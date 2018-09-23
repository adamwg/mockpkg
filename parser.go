package mockpkg

import (
	"errors"
	"go/ast"
	"go/build"
	"go/importer"
	"go/token"
	"go/types"
	"io/ioutil"
	"log"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/vektra/mockery/mockery"
	"golang.org/x/tools/go/loader"
)

// This is largely borrowed from mockery, tweaked to collect functions rather
// than interfaces and deal with both import paths and directory paths.

type Parser struct {
	configMapping  map[string][]*ast.File
	pathToFuncs    map[string][]string
	pathToASTFile  map[string]*ast.File
	parserPackages []*types.Package
	conf           loader.Config
	pkgPath        string
	pkgName        string
	desiredFuncs   []string
}

func NewParser(pkgPath string, funcs []string) *Parser {
	var conf loader.Config

	conf.TypeCheckFuncBodies = func(_ string) bool { return false }
	conf.TypeChecker.DisableUnusedImportCheck = true
	conf.TypeChecker.Importer = importer.Default()

	// Initialize the build context (e.g. GOARCH/GOOS fields) so we can use it for respecting
	// build tags during Parse.
	buildCtx := build.Default
	conf.Build = &buildCtx

	return &Parser{
		parserPackages: make([]*types.Package, 0),
		configMapping:  make(map[string][]*ast.File),
		pathToFuncs:    make(map[string][]string),
		pathToASTFile:  make(map[string]*ast.File),
		conf:           conf,
		pkgPath:        pkgPath,
		pkgName:        filepath.Base(pkgPath),
		desiredFuncs:   funcs,
	}
}

func (p *Parser) AddBuildTags(buildTags ...string) {
	p.conf.Build.BuildTags = append(p.conf.Build.BuildTags, buildTags...)
}

func (p *Parser) Parse() error {
	pkgPath := p.pkgPath

	// If not using an absolute path, see if it's relative or an import path.
	if !path.IsAbs(pkgPath) {
		st, err := os.Stat(pkgPath)
		if err != nil || !st.IsDir() {
			pkg, err := p.conf.Build.Import(pkgPath, "", 0)
			if err != nil {
				return err
			}
			pkgPath = pkg.Dir
		}
	}

	// To support relative paths to mock targets w/ vendor deps, we need to provide eventual
	// calls to build.Context.Import with an absolute path. It needs to be absolute because
	// Import will only find the vendor directory if our target path for parsing is under
	// a "root" (GOROOT or a GOPATH). Only absolute paths will pass the prefix-based validation.
	//
	// For example, if our parse target is "./ifaces", Import will check if any "roots" are a
	// prefix of "ifaces" and decide to skip the vendor search.
	pkgPath, err := filepath.Abs(pkgPath)
	if err != nil {
		return err
	}

	pkgPath, err = filepath.EvalSymlinks(pkgPath)
	if err != nil {
		return err
	}

	dir := pkgPath
	files, err := ioutil.ReadDir(pkgPath)
	if err != nil {
		return err
	}

	for _, fi := range files {
		if filepath.Ext(fi.Name()) != ".go" || strings.HasSuffix(fi.Name(), "_test.go") {
			continue
		}

		fname := fi.Name()
		fpath := filepath.Join(dir, fname)

		// If go/build would ignore this file, e.g. based on build tags, also ignore it here.
		//
		// (Further coupling with go internals and x/tools may of course bear a cost eventually
		// e.g. https://github.com/vektra/mockery/pull/117#issue-199337071, but should add
		// worthwhile consistency in this tool's behavior in the meantime.)
		match, matchErr := p.conf.Build.MatchFile(dir, fname)
		if matchErr != nil {
			return matchErr
		}
		if !match {
			continue
		}

		f, parseErr := p.conf.ParseFile(fpath, nil)
		if parseErr != nil {
			return parseErr
		}

		p.configMapping[pkgPath] = append(p.configMapping[pkgPath], f)
		p.pathToASTFile[fpath] = f
	}

	return nil
}

type NodeVisitor struct {
	declaredFuncs []string
}

func NewNodeVisitor() *NodeVisitor {
	return &NodeVisitor{
		declaredFuncs: make([]string, 0),
	}
}

func (n *NodeVisitor) DeclaredFuncs() []string {
	return n.declaredFuncs
}

func (nv *NodeVisitor) Visit(node ast.Node) ast.Visitor {
	switch n := node.(type) {
	case *ast.FuncDecl:
		if ast.IsExported(n.Name.Name) && n.Recv == nil {
			nv.declaredFuncs = append(nv.declaredFuncs, n.Name.Name)
		}
	}
	return nv
}

func (p *Parser) Load() error {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		for path, fi := range p.pathToASTFile {
			nv := NewNodeVisitor()
			ast.Walk(nv, fi)
			p.pathToFuncs[path] = nv.DeclaredFuncs()
		}
		wg.Done()
	}()

	// Type-check a package consisting of this file.
	// Type information for the imported packages
	// comes from $GOROOT/pkg/$GOOS_$GOOARCH/fmt.a.
	for path, files := range p.configMapping {
		p.conf.CreateFromFiles(path, files...)
	}

	prog, err := p.conf.Load()
	if err != nil {
		return err
	}

	for _, pkgInfo := range prog.Created {
		p.parserPackages = append(p.parserPackages, pkgInfo.Pkg)
	}

	wg.Wait()
	return nil
}

func (p *Parser) Interface() (*mockery.Interface, error) {
	if len(p.parserPackages) != 1 {
		return nil, errors.New("too many packages")
	}
	pkg := p.parserPackages[0]

	iface := &mockery.Interface{
		Name: strings.ToUpper(p.pkgName[0:1]) + p.pkgName[1:],
		Pkg:  p.parserPackages[0],
	}

	sort.Strings(p.desiredFuncs)

	var funcs []*types.Func
	for file, names := range p.pathToFuncs {
		ast := p.pathToASTFile[file]
		funcs = append(funcs, p.fileFuncs(pkg, ast, names)...)
	}
	if len(funcs) == 0 {
		return nil, errors.New("no functions for interface")
	}

	iface.Type = types.NewInterface(funcs, nil).Complete()
	typeName := types.NewTypeName(token.NoPos, pkg, iface.Name, iface.Type)
	iface.NamedType = types.NewNamed(typeName, iface.Type, funcs)

	return iface, nil
}

func (p *Parser) fileFuncs(pkg *types.Package, ast *ast.File, names []string) []*types.Func {
	scope := pkg.Scope()
	var funcs []*types.Func
	for _, name := range names {
		if len(p.desiredFuncs) > 0 {
			idx := sort.SearchStrings(p.desiredFuncs, name)
			if idx >= len(p.desiredFuncs) || p.desiredFuncs[idx] != name {
				// This function is not desired.
				continue
			}
		}

		obj := scope.Lookup(name)
		if obj == nil {
			log.Printf("failed to find function %s", name)
			continue
		}

		fn, ok := obj.(*types.Func)
		if !ok {
			log.Printf("found non-func %s", name)
			continue
		}

		funcs = append(funcs, fn)
	}

	return funcs
}
