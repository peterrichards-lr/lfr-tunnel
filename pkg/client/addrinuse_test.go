package client

import (
	"net"
	"testing"
)

// The inspector walks to the next port when one is taken. It recognised "taken" by the Unix error
// text, so on Windows -- where the message differs -- it failed instead of walking. Runs on every
// OS in CI, which is the point: the Windows job is the one that found it.
func TestIsAddrInUseRecognisesATakenPortOnThisOS(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() {
		if cerr := held.Close(); cerr != nil {
			t.Logf("closing: %v", cerr)
		}
	})
	_, err = net.Listen("tcp", held.Addr().String())
	if err == nil {
		t.Fatal("a second listen on a held port succeeded; cannot test")
	}
	if !isAddrInUse(err) {
		t.Errorf("isAddrInUse(%v) = false for a port that is held", err)
	}
}

func TestStartInspectorWalksPastATakenPort(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() {
		if cerr := held.Close(); cerr != nil {
			t.Logf("closing: %v", cerr)
		}
	})
	base := held.Addr().(*net.TCPAddr).Port
	got, err := StartInspector(base, NewInterceptorEngine("127.0.0.1", nil))
	if err != nil {
		t.Fatalf("StartInspector refused instead of walking past the held port %d: %v", base, err)
	}
	if got == base {
		t.Errorf("StartInspector reported the held port %d", base)
	}
}
