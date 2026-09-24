package configtest

// IsNonSourceDir reports whether a directory holds something other than this repository's own
// source, and so should not be descended into by a gate that scans it.
//
// The list is here, once, for the reason IsNestedWorktreeRoot is: the other half of every walk in
// this repository was copied five times and the copies did not agree (#2217). Measured at the
// time of writing:
//
//	client_token_writeback_test.go        .git node_modules vendor ui-dist bin
//	forwarded_header_boundary_test.go     .git node_modules vendor ui-dist bin dist
//	notification_failure_visibility_test  .git node_modules vendor
//	yaml_guard_test.go                    vendor node_modules ui-dist dist bin + every dot-directory
//	flags_documented_test.go              nothing (it walks docs/ only)
//
// Nothing made them agree and nothing said which differences were deliberate. At least one was
// not: the notification gate would have parsed any .go file dropped in bin/ or dist/ as
// repository source. That is #2211's root cause one field over -- a rule copied instead of
// shared, so the third, fourth and fifth walker had nowhere to pick it up from -- and it is
// fixed here before it costs anything rather than after.
//
// Genuine deviations stay at the call site rather than being folded in as options. yaml_guard's
// "and every dot-directory" is a real, wider rule and reads as one where it is written; burying
// it behind a flag here would make the shared answer mean two different things depending on who
// asked.
//
// ui-dist and dist are generated (`pkg/server/ui-dist` is content-hashed and must never be
// committed -- #1196), bin holds built binaries, vendor and node_modules are other people's
// source, and .git is not source at all.
func IsNonSourceDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "ui-dist", "dist", "bin":
		return true
	}
	return false
}
