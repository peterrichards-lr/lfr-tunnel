package server

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxTokenNameLength caps a Personal Access Token's name, in characters (#2348). Generous for a
// label like "Work Laptop" or "CI -- nightly build"; short enough that a name cannot crowd an admin
// table or an email subject. Both portals' inputs carry the same maxlength, so a user cannot type
// past what the server will accept.
const maxTokenNameLength = 100

// tokenNameError is a refusal shown to the person who typed the name -- both portals display the
// 400's message (V1 since #2348's review; it used to show a generic toast) -- so it reads as a
// sentence. Its own type rather than errors.New, whose
// convention (ST1005) is lowercase for errors that get wrapped; these never are.
type tokenNameError string

func (e tokenNameError) Error() string { return string(e) }

const (
	errTokenNameRequired tokenNameError = "Token name is required"
	// One message for every refused character rather than naming the one found: the caller
	// typed or pasted it, and "line breaks, tabs or other control characters" covers what they
	// will recognise.
	errTokenNameControl tokenNameError = "Token name cannot contain line breaks, tabs or other control characters"
)

var errTokenNameTooLong = tokenNameError(fmt.Sprintf("Token name must be %d characters or fewer", maxTokenNameLength))

// validateTokenName returns the name to store, trimmed, or why it cannot be stored (#2348).
//
// One function for both create paths -- handleCreateToken, which is routed, and
// portalService.CreateToken, which is not today -- because two copies of a rule are how one path
// comes to enforce it and the other not (#2264/#2267's role gate).
//
// Refused characters are refused, not stripped -- inside the name. A name reaches emails, both
// portals, audit rows and the client's advice lines; the mail sender is already safe against them
// (#2344 review), but a line break in a name can still forge a second line in a portal table or a
// log, and silently rewriting what the user typed would hide that it was refused. At the ENDS,
// TrimSpace removes whitespace -- which includes "\n" and U+0085 -- as it removes spaces: a
// trailing newline from a paste is not an attempt to forge anything, and what is stored has none.
func validateTokenName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", errTokenNameRequired
	}
	if utf8.RuneCountInString(name) > maxTokenNameLength {
		return "", errTokenNameTooLong
	}
	for _, r := range name {
		if refusedInTokenName(r) {
			return "", errTokenNameControl
		}
	}
	return name, nil
}

// refusedInTokenName is the set of characters a name may not contain.
//
//   - Control characters (C0, DEL, C1). unicode.IsControl covers exactly these: it returns false
//     for everything above U+00FF.
//   - U+2028 LINE SEPARATOR and U+2029 PARAGRAPH SEPARATOR (Zl, Zp), which IsControl misses and
//     browsers render as forced line breaks -- the same "second line" a "\n" would forge.
//   - Bidirectional controls (U+061C, U+200E-200F, U+202A-202E, U+2066-2069), which reorder how a
//     name displays in an admin queue or an email subject without changing what it is.
//
// Deliberately NOT all of category Cf: ZWJ (U+200D) joins emoji sequences and ZWNJ (U+200C) is
// ordinary in Persian and Indic text, and refusing them would refuse real names.
func refusedInTokenName(r rune) bool {
	return unicode.IsControl(r) || unicode.In(r, unicode.Zl, unicode.Zp, unicode.Bidi_Control)
}
