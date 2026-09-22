package client

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"
)

// A cancelled request must not be logged as a proxy error (#2165).
//
// context.Canceled means the request went away -- the session was torn down for a region move,
// or the visitor closed the tab. Both are ordinary, and one of them happens in a BURST exactly
// when the client is moving between gateways.
//
// That burst was drowning the line that explains the move. The session loop logs "Connection to
// region 'x' lost. Performing dynamic region failover..." once, and the TUI's SYSTEM LOGS panel
// is a few lines tall; several "http: proxy error: context canceled" immediately afterwards push
// it out of view. On 2026-09-22 a user watching that panel during a fleet deploy saw three of
// them, no explanation, and reported the tunnel going down "out of nowhere".
//
// Asserted on the source rather than by driving a live proxy: building one needs a listening
// target, an engine and a request in flight, and the thing worth protecting is the single
// decision of whether this error is printed.
func TestACancelledRequestIsNotLoggedAsAProxyError(t *testing.T) {
	src, err := os.ReadFile("interceptor.go")
	if err != nil {
		t.Fatalf("read interceptor.go: %v", err)
	}

	// Comments stripped first: this file explains the old behaviour in prose immediately above
	// the code, and a scan that cannot tell an explanation from an instruction reports the
	// opposite of the truth -- which it did, twice, earlier in this codebase's history.
	text := stripGoLineComments(string(src))

	handler := text[strings.Index(text, "proxy.ErrorHandler = func("):]
	if end := strings.Index(handler, "\n\t}"); end > 0 {
		handler = handler[:end]
	}
	if handler == "" {
		t.Fatal("proxy.ErrorHandler is gone; point this check at whatever replaced it")
	}

	if !strings.Contains(handler, "errors.Is(err, context.Canceled)") {
		t.Error("the proxy error handler prints every error, including context.Canceled. " +
			"A region move cancels the in-flight requests, so the burst of these buries the " +
			"one line that explains the move.")
	}
}

// ...and every other error still prints. A dial failure means nothing is listening on the local
// port, which is the case this handler exists for (#980) and is worth seeing.
func TestOtherProxyErrorsStillPrint(t *testing.T) {
	src, err := os.ReadFile("interceptor.go")
	if err != nil {
		t.Fatalf("read interceptor.go: %v", err)
	}
	text := stripGoLineComments(string(src))

	handler := text[strings.Index(text, "proxy.ErrorHandler = func("):]
	if end := strings.Index(handler, "\n\t}"); end > 0 {
		handler = handler[:end]
	}

	if !strings.Contains(handler, `log.Printf("http: proxy error: %v", err)`) {
		t.Error("nothing logs a proxy error any more. Suppressing the cancelled case should " +
			"not suppress a dial failure, which is the one this handler was written for.")
	}
}

// The behaviour itself, on the decision in isolation: a cancelled context is filtered and a real
// failure is not. Guards against the filter being widened to swallow everything.
func TestOnlyCancellationIsFiltered(t *testing.T) {
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(old)

	report := func(err error) {
		if !errors.Is(err, context.Canceled) {
			log.Printf("http: proxy error: %v", err)
		}
	}

	report(context.Canceled)
	if buf.Len() != 0 {
		t.Errorf("a cancelled request was logged: %q", buf.String())
	}

	// Wrapped, as the net/http stack delivers it in practice.
	buf.Reset()
	report(fmt.Errorf("proxying to localhost:8080: %w", context.Canceled))
	if buf.Len() != 0 {
		t.Errorf("a WRAPPED cancellation was logged; errors.Is is required, not ==: %q", buf.String())
	}

	buf.Reset()
	report(errors.New("dial tcp 127.0.0.1:8080: connect: connection refused"))
	if !strings.Contains(buf.String(), "connection refused") {
		t.Error("a dial failure was not logged; that is the case this handler exists for")
	}
}

// stripGoLineComments removes // comments so a source assertion cannot match prose.
func stripGoLineComments(src string) string {
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}
