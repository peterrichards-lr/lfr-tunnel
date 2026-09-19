package main

import (
	"flag"
	"os"
	"strings"
	"testing"
)

// The routing flags were named after what they TAKE rather than what they DO (#2061).
//
// -server and -gateway are synonyms in ordinary use while differing on the single most
// consequential thing the client has -- whether failover exists -- and -region under-promised so
// badly that the owner asked for a "preferred node" flag that already existed. The cost is in the
// issue history: #1691 was filed as a bug ("-server silently disables region election and
// failover"), #1694 added a whole new flag because -server did the wrong thing for the common
// case, and #1695 was a pure help-text fix for the same confusion.
//
// Renaming them is only safe if the old spellings keep working, so that is what these cover.

func TestADeprecatedFlagStillSetsTheValue(t *testing.T) {
	newVal, oldVal := "", "https://gw.example.com"

	if err := resolveDeprecatedFlag("pin", &newVal, "server", &oldVal); err != nil {
		t.Fatalf("resolveDeprecatedFlag: %v", err)
	}
	if newVal != "https://gw.example.com" {
		t.Errorf("the old spelling did not carry through: got %q -- someone's existing script "+
			"would silently stop pinning", newVal)
	}
}

func TestTheNewFlagIsLeftAloneWhenTheOldOneIsAbsent(t *testing.T) {
	newVal, oldVal := "https://new.example.com", ""

	if err := resolveDeprecatedFlag("pin", &newVal, "server", &oldVal); err != nil {
		t.Fatalf("resolveDeprecatedFlag: %v", err)
	}
	if newVal != "https://new.example.com" {
		t.Errorf("the new spelling was clobbered by an absent old one: got %q", newVal)
	}
}

// Both spellings with DIFFERENT values must refuse rather than pick one. Silently choosing is
// how -server came to pin a US user to a European gateway for the life of a tunnel (#1691).
func TestGivingBothSpellingsWithDifferentValuesIsRefused(t *testing.T) {
	newVal, oldVal := "https://a.example.com", "https://b.example.com"

	err := resolveDeprecatedFlag("pin", &newVal, "server", &oldVal)
	if err == nil {
		t.Fatal("two different values for the same setting were accepted -- one of them is " +
			"being silently ignored, and the user cannot tell which")
	}
	for _, want := range []string{"-pin", "-server", "a.example.com", "b.example.com"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q, so it does not say what to change: %v",
				want, err)
		}
	}
}

// The same value twice is not a contradiction -- a script that sets both during a migration
// should keep working.
func TestGivingBothSpellingsWithTheSameValueIsFine(t *testing.T) {
	newVal, oldVal := "https://same.example.com", "https://same.example.com"

	if err := resolveDeprecatedFlag("pin", &newVal, "server", &oldVal); err != nil {
		t.Errorf("identical values were refused: %v", err)
	}
}

// TestEveryOldSpellingIsStillRegistered is the compatibility guard: renaming a flag without
// leaving the old name behind breaks every script that uses it, silently -- Go's flag package
// exits with "flag provided but not defined".
func TestEveryOldSpellingIsStillRegistered(t *testing.T) {
	for old, replacement := range map[string]string{
		"server":  "pin",
		"gateway": "bootstrap",
		"region":  "prefer-region",
	} {
		f := flag.Lookup(old)
		if f == nil {
			t.Errorf("-%s is no longer registered; every script using it would fail with "+
				"\"flag provided but not defined\". Keep it as a deprecated alias for -%s.",
				old, replacement)
			continue
		}
		if !strings.Contains(f.Usage, "DEPRECATED") {
			t.Errorf("-%s is registered but its help does not say DEPRECATED, so nobody learns "+
				"to move to -%s", old, replacement)
		}
		if flag.Lookup(replacement) == nil {
			t.Errorf("-%s is deprecated in favour of -%s, which is not registered", old, replacement)
		}
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
