// Package httputil holds the HTTP policy shared by every dotular download —
// registry modules, remote scripts, and released binaries. It lives in its own
// leaf package because internal/registry and internal/actions are siblings that
// do not otherwise depend on each other.
package httputil

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

// Timeout bounds a download end to end. http.DefaultClient has none and every
// caller passes context.Background(), so without this a hung or slow-loris
// server stalls the run indefinitely — and dotular has no signal handling to
// interrupt it with.
const Timeout = 60 * time.Second

// MaxBodySize caps a response that is read into memory. Module definitions and
// shell scripts are kilobytes; anything approaching this ceiling is a wrong URL
// or a hostile server, not a module. Binaries stream to disk and are not capped.
const MaxBodySize = 16 << 20 // 16 MiB

// Client is the client every download goes through, so that the timeout cannot
// be forgotten at a new call site.
var Client = &http.Client{Timeout: Timeout}

// ReadBody reads a response body into memory, refusing one over MaxBodySize
// rather than growing the buffer until the process is killed.
func ReadBody(r io.Reader) ([]byte, error) {
	// +1 so that hitting the limit exactly is distinguishable from exceeding it.
	data, err := io.ReadAll(io.LimitReader(r, MaxBodySize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxBodySize {
		return nil, fmt.Errorf("response body exceeds the %d byte limit", MaxBodySize)
	}
	return data, nil
}
