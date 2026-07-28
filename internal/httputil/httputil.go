// Package httputil holds the HTTP policy shared by every dotular download —
// registry modules, remote scripts, and released binaries. It lives in its own
// leaf package because internal/registry and internal/actions are siblings that
// do not otherwise depend on each other.
package httputil

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
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
//     bounding is a gap in progress. That is StreamClient.
//
// Give StreamClient an end-to-end Timeout and a real 80 MB binary on a slow
// link fails; drop Client's and an unresponsive server hangs the run forever
// (no caller sets a context deadline, and dotular has no signal handling).

// Timeout bounds an in-memory download end to end. http.DefaultClient has none.
const Timeout = 60 * time.Second

// StallTimeout bounds every phase of a streamed download in which a healthy
// server makes progress: connecting, the TLS handshake, producing response
// headers, and each read of the body. Total duration stays unbounded on purpose
// — progress is the health signal, elapsed time is not — so a legitimately large
// binary on a slow link still completes.
const StallTimeout = 30 * time.Second

// ErrStalled reports that a transfer went quiet for StallTimeout. It is its own
// error so a caller can say a download stalled rather than merely failed.
var ErrStalled = errors.New("transfer stalled")

// MaxBodySize caps a response that is read into memory. Module definitions and
// shell scripts are kilobytes; anything approaching this ceiling is a wrong URL
// or a hostile server, not a module. Streamed downloads go to disk rather than
// the heap and are not capped.
const MaxBodySize = 16 << 20 // 16 MiB

// Client is for a body read wholly into memory, via ReadBody.
var Client = &http.Client{Timeout: Timeout}

// StreamClient is for a body copied straight to disk. It sets no Client.Timeout
// on purpose; its limits live on the transport and on the connection underneath.
var StreamClient = &http.Client{Transport: newStreamTransport(StallTimeout)}

// newStreamTransport is parameterised so that tests can exercise this policy at
// millisecond scale rather than waiting out StallTimeout.
func newStreamTransport(stall time.Duration) *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := (&net.Dialer{Timeout: stall}).DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			return &stallConn{Conn: conn, stall: stall}, nil
		},
		TLSHandshakeTimeout: stall,
		// Redundant with stallConn for a server that goes fully silent, but it
		// also caps one that dribbles header bytes forever — every byte resets a
		// read deadline, so only a bound on the whole phase catches that.
		ResponseHeaderTimeout: stall,
		IdleConnTimeout:       stall,
	}
}

// stallConn arms a read deadline before every read, so the deadline is in effect
// reset each time bytes arrive: it bounds a gap in progress, not the transfer.
//
// This lives at the connection rather than in a wrapper around resp.Body
// because a Read already parked on a silent socket cannot be freed from above —
// only the connection can time it out. It also means the body is covered by the
// same limit as the headers, with no timer or goroutine per download to leak.
type stallConn struct {
	net.Conn
	stall time.Duration
}

func (c *stallConn) Read(p []byte) (int, error) {
	if err := c.Conn.SetReadDeadline(time.Now().Add(c.stall)); err != nil {
		return 0, err
	}
	n, err := c.Conn.Read(p)
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return n, fmt.Errorf("%w: no data for %s", ErrStalled, c.stall)
	}
	return n, err
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
