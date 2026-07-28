// Package httputil holds the HTTP policy shared by every dotular download —
// registry modules, remote scripts, and released binaries. It lives in its own
// leaf package because internal/registry and internal/actions are siblings that
// do not otherwise depend on each other.
package httputil

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// There are two clients here because the two kinds of download have different
// notions of "too long", and every attempt to collapse them into one breaks one
// of the two:
//
//   - Read into memory (registry modules, remote scripts) — small by
//     construction, so total elapsed time is a valid bound and the body needs a
//     size cap. That is Client.
//   - Streamed to disk (released binaries) — legitimately tens of megabytes and
//     minutes long, so total elapsed time bounds nothing useful. What is worth
//     bounding is a stall. That is StreamClient.
//
// Give StreamClient an end-to-end Timeout and a real 80 MB binary on a slow
// link fails; drop Client's and an unresponsive server hangs the run forever
// (no caller sets a context deadline, and dotular has no signal handling).

// Timeout bounds an in-memory download end to end. http.DefaultClient has none.
const Timeout = 60 * time.Second

// StallTimeout bounds the phases of a streamed download in which a healthy
// server always makes progress: connecting, the TLS handshake, and producing
// response headers. The body itself is deliberately unbounded in time.
const StallTimeout = 30 * time.Second

// MaxBodySize caps a response that is read into memory. Module definitions and
// shell scripts are kilobytes; anything approaching this ceiling is a wrong URL
// or a hostile server, not a module. Streamed downloads go to disk rather than
// the heap and are not capped.
const MaxBodySize = 16 << 20 // 16 MiB

// Client is for a body read wholly into memory, via ReadBody.
var Client = &http.Client{Timeout: Timeout}

// StreamClient is for a body copied straight to disk. It sets no Client.Timeout
// on purpose; its limits live on the transport instead.
var StreamClient = &http.Client{Transport: newStreamTransport(StallTimeout)}

// newStreamTransport is parameterised so that tests can exercise this policy at
// millisecond scale rather than waiting out StallTimeout.
func newStreamTransport(stall time.Duration) *http.Transport {
	return &http.Transport{
		DialContext:           (&net.Dialer{Timeout: stall}).DialContext,
		TLSHandshakeTimeout:   stall,
		ResponseHeaderTimeout: stall,
		IdleConnTimeout:       stall,
	}
}

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
