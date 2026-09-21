package client

import (
	"strings"
	"testing"
)

// The name a pid file is WRITTEN under must be the name things look for.
//
// It was not. getPIDFilePath wrote "lfr-tunnel-<sub>.pid"; the upgrade path scanned for
// "client-*.pid", which is the LOG file's prefix. The two never matched, so `--upgrade`
// terminated no background tunnel at all -- for as long as that code existed -- while printing
// "Upgrade successful!" (#2128).
//
// The cost was never the stale process. It was that an upgrade reported success while the old
// image kept serving, so a fix that had just been installed appeared not to work. That reading
// was reached twice in one afternoon on 2026-09-21 and sent the investigation back into code
// that was already correct.

// The pairing, asserted directly. Either half alone would have passed while the bug was live.
func TestAPIDFileIsFoundUnderTheNameItIsWrittenAs(t *testing.T) {
	for _, sub := range []string{"peters", "gatxdemo", "dxplive", "a-b-c", "x"} {
		name := TunnelPIDFileName(sub)

		got, ok := TunnelPIDFileSubdomain(name)
		if !ok {
			t.Errorf("TunnelPIDFileName(%q) produced %q, which TunnelPIDFileSubdomain does not "+
				"recognise as a tunnel pid file -- that is exactly the mismatch that made "+
				"--upgrade a no-op", sub, name)
			continue
		}
		if got != sub {
			t.Errorf("round trip for %q gave %q via %q", sub, got, name)
		}
	}
}

// The specific wrong prefix, named, so nobody reintroduces it by copying from the log files.
func TestTheLogFilePrefixIsNotAPIDFile(t *testing.T) {
	if _, ok := TunnelPIDFileSubdomain("client-peters.pid"); ok {
		t.Error("\"client-peters.pid\" was accepted as a tunnel pid file. That is the LOG file's " +
			"prefix, and matching it would hide the real files rather than find them")
	}
	if strings.HasPrefix(TunnelPIDFileName("peters"), "client-") {
		t.Error("tunnel pid files are being written under the log file's prefix")
	}
}

// Not a pid file, and not to be treated as one.
func TestOtherFilesInTheStateDirectoryAreNotMistakenForPIDs(t *testing.T) {
	for _, name := range []string{
		"client-peters.log",     // the console log
		"lfr-tunnel-peters.log", // a log that merely shares the prefix
		"gui.pid",               // the tray, which has its own path
		"lfr-tunnel-.pid",       // no subdomain at all
		"config.yaml",
		"token",
	} {
		if sub, ok := TunnelPIDFileSubdomain(name); ok {
			t.Errorf("%q was read as the tunnel pid file for subdomain %q", name, sub)
		}
	}
}

// The tray records itself somewhere both --upgrade and -stop can find it. One path, not three.
func TestTheTrayPIDPathIsSharedNotRetyped(t *testing.T) {
	path, err := GUIPIDPath()
	if err != nil {
		t.Fatalf("GUIPIDPath: %v", err)
	}
	if !strings.HasSuffix(path, "gui.pid") {
		t.Errorf("GUIPIDPath() = %q, which is not the file the tray writes", path)
	}
	if !strings.Contains(path, ".lfr-tunnel") {
		t.Errorf("GUIPIDPath() = %q, outside the state directory", path)
	}
}
