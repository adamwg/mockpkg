# mockpkg

`mockpkg` uses [`mockery`](https://github.com/vektra/mockery) to generate mocks
for a package as if the package were an interface.

## Usage

```
Usage: mockpkg [options] <package> [<func1> <func2> ...]
  -outfile string
        file to write mocks to; if empty output to stdout
  -overwrite
        overwrite the destination file if it exists
  -tags string
        space-separated list of additional build tags to use
```

## Example

Suppose you number your hosts using suffixes in their short hostnames (e.g.,
`node01.example.com`), and have a package to return the node number:

```go
package hostnum

var (
	errNoNumber = errors.New("no number in hostname")
)

func FromHostname() (int, error) {
	hn, err := os.Hostname()
	if err != nil {
		return 0, err
	}
	short := strings.Split(hn, ".")[0]
	re := regexp.MustCompile(`.+(\d+)$`)
	m := re.FindStringSubmatch(short)
	if len(m) < 1 {
		return 0, errNoNumber
	}

	return strconv.Atoi(m[1])
}
```

You'd like to write unit tests for this package, but since it uses
`os.Hostname`, you'd have to run it on a bunch of hosts to effectively test all
the cases.

By making the hostname function a non-exported variable in the package, you can
inject it from tests:

```go
package hostnum

var (
	errNoNumber = errors.New("no number in hostname")
	hostnameFn  = os.Hostname
)

func FromHostname() (int, error) {
	hn, err := hostnameFn()
	if err != nil {
		return 0, err
	}
	short := strings.Split(hn, ".")[0]
	re := regexp.MustCompile(`.+(\d+)$`)
	m := re.FindStringSubmatch(short)
	if len(m) < 1 {
		return 0, errNoNumber
	}

	return strconv.Atoi(m[1])
}
```

`mockpkg` can be used to generate a mock for the hostname function:

```console
$ mockpkg -outfile mocks/hostname.go os Hostname
```

And the resulting mock can be injected for tests:

```go
package hostnum

import (
	"testing"

	"example.com/hostnum/mocks"
	"github.com/stretchr/testify/assert"
)

func TestFromHostname(t *testing.T) {
	mockOS := &mocks.Os{}
	hostnameFn = mockOS.Hostname

	t.Run("host 1", func(t *testing.T) {
		mockOS.On("Hostname").Return("host01.example.com", nil).Once()
		n, err := FromHostname()

		assert.NoError(t, err)
		assert.Equal(t, 1, n)
	})
}
```

## See also

* `mockpkg` uses [`mockery`](https://github.com/vektra/mockery) to perform the
  actual mock generation.
* The generated mocks
  use [`testify/mock`](https://godoc.org/github.com/stretchr/testify/mock).
