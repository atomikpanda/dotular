package httputil

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestClientHasTimeout(t *testing.T) {
	if Client.Timeout == 0 {
		t.Fatal("Client.Timeout = 0; the shared client exists precisely so no download is unbounded")
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
