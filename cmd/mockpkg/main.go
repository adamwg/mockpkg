package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/vektra/mockery/mockery"

	"github.com/adamwg/mockpkg"
)

var usageFmt = `%s - Generate mocks for all or some of a package's exported functions.

Usage: %s [options] <package> [<func1> <func2> ...]
`

func usage() {
	fmt.Printf(usageFmt, os.Args[0], os.Args[0])
	flag.PrintDefaults()
	os.Exit(1)
}

func main() {
	var (
		outFile   = flag.String("outfile", "", "file to write mocks to; if empty output to stdout")
		overwrite = flag.Bool("overwrite", false, "overwrite the destination file if it exists")
		buildTags = flag.String("tags", "", "space-separated list of additional build tags to use")
	)
	flag.Parse()

	if len(flag.Args()) < 1 {
		usage()
	}

	out := os.Stdout
	if *outFile != "" {
		_, err := os.Stat(*outFile)
		if !os.IsNotExist(err) && !*overwrite {
			log.Fatal("output file exists; use -overwrite to overwrite")
		}
		f, err := os.OpenFile(*outFile, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
		if err != nil {
			log.Fatalf("failed to open output file: %v", err)
		}
		out = f
	}

	path := flag.Arg(0)
	funcs := flag.Args()[1:]

	pars := mockpkg.NewParser(path, funcs)
	if *buildTags != "" {
		pars.AddBuildTags(strings.Split(*buildTags, " ")...)
	}

	if err := pars.Parse(); err != nil {
		log.Fatalf("parse error: %v", err)
	}
	if err := pars.Load(); err != nil {
		log.Fatalf("load error: %v", err)
	}

	iface, err := pars.Interface()
	if err != nil {
		log.Fatalf("iface error: %v", err)
	}

	pkg := iface.Pkg.Path()
	gen := mockery.NewGenerator(iface, pkg, false, "")
	gen.GeneratePrologueNote("")
	gen.GeneratePrologue("mocks")
	if err := gen.Generate(); err != nil {
		log.Fatalf("generate error: %v", err)
	}

	if err := gen.Write(out); err != nil {
		log.Fatalf("write error: %v", err)
	}
}
