// Package configtest renders values for the config files this repo's tests write by hand, so
// that a path on Windows does not change what the config says (#2029).
//
// A non-_test.go file in a package whose name says it is test support, after net/http/httptest
// and pkg/geo/geotest (#2014) -- the only shape Go has for sharing a helper like this across
// packages. It lives under pkg/config because every caller is writing a file that
// config.LoadClientConfig or config.LoadServerConfig will read back.
//
// It is imported by tests in pkg/config, pkg/server and cmd/lfr-tunnel. It is deliberately not
// imported by any production code, and holds nothing production needs.
//
// It is also where IsNestedWorktreeRoot lives (worktree.go), for the same reason: it is a rule
// every tree-walking gate in this repository needs and no single package owns (#2211).
package configtest

import "strings"

// SingleQuoted renders v as a YAML scalar that parses back to exactly v.
//
// The hazard it exists for is Windows paths. A test that embeds a temp directory into a config
// file is embedding `C:\Users\RUNNER~1\AppData\...`, and inside a DOUBLE-quoted YAML scalar
// `\U` is an escape introducing an 8-digit hex code point -- so the parser rejects the whole
// file, `yaml: line 2: did not find expected hexdecimal number`, before reaching the key under
// test. Tests then fail on Windows only, for a reason that has nothing to do with their subject.
//
// This has now broken master three times: eight sites in #1775, two more in #1773 that used
// different variable names, and pkg/server's geo reload tests in #2029 after #2022 wrote the
// same shape in a package the first two fixes never looked at.
//
// Single-quoted YAML performs no escape processing at all, so the value goes in verbatim. The
// only character with meaning there is `'`, escaped by doubling it; a path may legally contain
// one, so that is handled rather than assumed away.
//
// It takes any string rather than only a path. The guard in this package's tests rejects
// concatenation into a double-quoted scalar outright, without judging whether the value being
// concatenated happens to be path-shaped -- judging that by eye is exactly what let #1775
// through twice.
func SingleQuoted(v string) string {
	return "'" + strings.ReplaceAll(v, "'", "''") + "'"
}
