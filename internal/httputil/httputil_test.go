package httputil

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientHasTimeout(t *testing.T) {
	if Client.Timeout == 0 {
		t.Fatal("Client.Timeout = 0; the in-memory client exists precisely so no download is unbounded")
	}
}

// Guards the split itself: an end-to-end Timeout on StreamClient would abort a
// large but healthy binary download, which is the whole reason it is separate.
func TestStreamClientBoundsStallsNotDuration(t *testing.T) {
	if StreamClient.Timeout != 0 {
		t.Errorf("StreamClient.Timeout = %v, want 0 — a streamed body must not be bounded by total duration", StreamClient.Timeout)
	}
	tr, ok := StreamClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("StreamClient.Transport = %T, want *http.Transport carrying the stall limits", StreamClient.Transport)
	}
	if tr.ResponseHeaderTimeout == 0 {
		t.Error("ResponseHeaderTimeout = 0; a server that connects and then goes quiet must still fail fast")
	}
	if tr.DialContext == nil {
		t.Error("DialContext = nil; the dial timeout is what bounds an unreachable host")
	}
}

// A body that arrives slowly but steadily must complete. The same response
// through an end-to-end-bounded client fails, which is the failure mode the
// streaming policy exists to avoid — shown here at millisecond scale.
func TestStreamClientCompletesSlowSteadyBody(t *testing.T) {
	const chunks = 5
	const perChunk = 20 * time.Millisecond

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < chunks; i++ {
			fmt.Fprint(w, "chunk")
			w.(http.Flusher).Flush()
			time.Sleep(perChunk)
		}
	}))
	defer srv.Close()

	body := func(c *http.Client) (string, error) {
		resp, err := c.Get(srv.URL)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		return string(data), err
	}

	got, err := body(&http.Client{Transport: newStreamTransport(StallTimeout)})
	if err != nil {
		t.Fatalf("streaming policy failed on a slow but progressing body: %v", err)
	}
	if want := strings.Repeat("chunk", chunks); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}

	if _, err := body(&http.Client{Timeout: perChunk}); err == nil {
		t.Error("an end-to-end timeout accepted a body longer than its window; the contrast this test relies on is gone")
	}
}

// A server that accepts the connection and then never answers must fail fast
// rather than hang the run.
func TestStreamTransportFailsFastOnAStalledServer(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer func() {
		close(release)
		srv.Close()
	}()

	c := &http.Client{Transport: newStreamTransport(20 * time.Millisecond)}
	if _, err := c.Get(srv.URL); err == nil {
		t.Fatal("Get() = nil error, want a timeout from a server that never sends response headers")
	}
}

func TestReadBodyReturnsWholeBody(t *testing.T) {
	got, err := ReadBody(strings.NewReader("name: mod\n"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "name: mod\n" {
		t.Errorf("ReadBody() = %q, want %q", got, "name: mod\n")
	}
}

// endlessReader stands in for a server that never stops sending, without
// materialising a fixture of that size.
type endlessReader struct{}

func (endlessReader) Read(p []byte) (int, error) { return len(p), nil }

func TestReadBodyRejectsOversizedBody(t *testing.T) {
	_, err := ReadBody(endlessReader{})
	if err == nil {
		t.Fatal("ReadBody() = nil error, want a refusal once the body passes MaxBodySize")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("ReadBody() error = %q, want it to name the size limit", err)
	}
}

// A read that fails mid-body must surface, not be reported as a short success:
// a truncated module still parses as valid YAML.
func TestReadBodyPropagatesReadError(t *testing.T) {
	want := errors.New("connection reset")
	_, err := ReadBody(io.MultiReader(strings.NewReader("name: "), errReader{want}))
	if !errors.Is(err, want) {
		t.Errorf("ReadBody() error = %v, want %v", err, want)
	}
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }
