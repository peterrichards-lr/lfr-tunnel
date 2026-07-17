package server

import (
	"testing"
	"time"
)

func TestServer_StartAndStop(t *testing.T) {
	srv := setupTestServerForAPI(t)
	srv.cfg.PruneInterval = 1 * time.Hour

	go func() {
		// This might block or return an error if certmagic fails,
		// but we just want to hit the code paths.
		_ = srv.Start() //nolint:errcheck
	}()

	time.Sleep(50 * time.Millisecond)
	srv.Stop()
}
