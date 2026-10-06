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
// 400's message as it is -- so it reads as a sentence. Its own type rather than errors.New, whose
// convention (ST1005) is lowercase for errors that get wrapped; these never are.
type tokenNameError string

func (e tokenNameError) Error() string { return string(e) }

const (
	errTokenNameRequired tokenNameError = "Token name is required"
	// One message for every control character rather than naming the one found: the caller
	// typed it, and "line break, tab or other control character" covers what they will
	// recognise.
	errTokenNameControl tokenNameError = "Token name cannot contain line breaks, tabs or other control characters"
)

var errTokenNameTooLong = tokenNameError(fmt.Sprintf("Token name must be %d characters or fewer", maxTokenNameLength))

// validateTokenName returns the name to store, trimmed, or why it cannot be stored (#2348).
//
// One function for both create paths -- handleCreateToken, which is routed, and
// portalService.CreateToken, which is not today -- because two copies of a rule are how one path
// comes to enforce it and the other not (#2264/#2267's role gate).
//
// Control characters are refused, not stripped. A name reaches emails, both portals, audit rows
// and the client's advice lines; the mail sender is already safe against them (#2344 review), but
// a newline in a name can still forge a second line in a log or audit view. Silently rewriting
// what the user typed would hide that it was refused.
func validateTokenName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", errTokenNameRequired
	}
	if utf8.RuneCountInString(name) > maxTokenNameLength {
		return "", errTokenNameTooLong
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", errTokenNameControl
		}
	}
	return name, nil
}
