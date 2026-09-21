package client

import (
	"os"
	"path/filepath"
	"strings"
)

// The pid files this tool writes, named in ONE place.
//
// They were named in three: getPIDFilePath wrote "lfr-tunnel-<sub>.pid", getActiveSubdomains
// matched that correctly, and the upgrade path looked for "client-*.pid" -- the LOG file's
// prefix. The two never matched, so `--upgrade` silently terminated no background tunnel at
// all, for as long as that code has existed, while reporting success (#2128).
//
// The cost was not the stale process itself but what it implied: an upgrade reports success,
// the old image keeps serving, and the fix that was just installed appears not to work. On
// 2026-09-21 that reading was reached twice in one afternoon, and sent us back to code that
// was already correct.
const (
	tunnelPIDPrefix = "lfr-tunnel-"
	pidSuffix       = ".pid"
	guiPIDName      = "gui.pid"
)

// stateDir is where every pid file lives.
func stateDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".lfr-tunnel"), nil
}

// TunnelPIDFileName is the only place a tunnel's pid file name is constructed. Anything that
// looks for one must go through TunnelPIDFileSubdomain, so the two cannot drift apart again.
func TunnelPIDFileName(subdomain string) string {
	safe := strings.ReplaceAll(subdomain, "/", "-")
	safe = strings.ReplaceAll(safe, "\\", "-")
	return tunnelPIDPrefix + safe + pidSuffix
}

// TunnelPIDFileSubdomain reports whether a directory entry is a tunnel pid file, and for which
// subdomain. The inverse of TunnelPIDFileName, deliberately adjacent to it.
func TunnelPIDFileSubdomain(name string) (string, bool) {
	if !strings.HasPrefix(name, tunnelPIDPrefix) || !strings.HasSuffix(name, pidSuffix) {
		return "", false
	}
	sub := strings.TrimSuffix(strings.TrimPrefix(name, tunnelPIDPrefix), pidSuffix)
	if sub == "" {
		return "", false
	}
	return sub, true
}

// GUIPIDPath is where the tray records itself. The tray writes it; upgrade and stop read it.
func GUIPIDPath() (string, error) {
	dir, err := stateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, guiPIDName), nil
}

// ActiveTunnelPIDFiles returns every tunnel pid file present, keyed by subdomain.
func ActiveTunnelPIDFiles() (map[string]string, error) {
	dir, err := stateDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := map[string]string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if sub, ok := TunnelPIDFileSubdomain(e.Name()); ok {
			out[sub] = filepath.Join(dir, e.Name())
		}
	}
	return out, nil
}
