package main

import (
	"flag"
	"testing"
)

// The Settings panel decides whether a field is editable from this map, so a flag named here
// that does not exist means a field stays editable while a flag quietly overrides it on every
// start -- the exact defect #2088 is about, re-opened by a rename.
func TestEveryNamedFlagExists(t *testing.T) {
	defined := make(map[string]bool)
	flag.VisitAll(func(f *flag.Flag) { defined[f.Name] = true })

	for _, source := range settingSources {
		if len(source.Flags) == 0 && len(source.EnvVars) == 0 {
			t.Errorf("%q names neither a flag nor an environment variable, so nothing can ever "+
				"mark it overridden", source.Key)
		}
		for _, name := range source.Flags {
			if !defined[name] {
				t.Errorf("%q lists flag -%s, which this binary does not define. A renamed flag "+
					"stops being detected here silently, and the field goes back to looking "+
					"editable while the flag still wins at every start.", source.Key, name)
			}
		}
	}
}

// clearLaunchEnv unsets every variable the map consults, so a test says what it means.
//
// Written after this test failed on the maintainer's own machine, which exports
// LFT_CLIENT_TOKEN -- the function was right and the test's premise ("no environment") was
// false. That is also a live example of the defect: on that machine the auth token really is
// claimed at every start, so the panel's token field can never be changed from the panel.
func clearLaunchEnv(t *testing.T) {
	t.Helper()
	for _, source := range settingSources {
		for _, env := range source.EnvVars {
			t.Setenv(env, "")
		}
	}
}

// PREMISE plus the reported behaviour: an unset flag must not claim the field, or every field
// would be locked and the panel would be useless.
func TestNothingIsOverriddenWhenNothingWasGiven(t *testing.T) {
	clearLaunchEnv(t)

	if got := launchOverrides(); len(got) != 0 {
		t.Errorf("with no flags set and no environment, launchOverrides reported %v.\n"+
			"flag.Visit must report only flags actually given -- a flag sitting at its default "+
			"has not taken the setting over", got)
	}
}

func TestAnEnvironmentVariableClaimsTheField(t *testing.T) {
	clearLaunchEnv(t)
	t.Setenv("LFT_CLIENT_SUBDOMAIN", "from-env")

	got := launchOverrides()
	if got["subdomain"] != "LFT_CLIENT_SUBDOMAIN" {
		t.Errorf("subdomain should be reported as claimed by LFT_CLIENT_SUBDOMAIN, got %q.\n"+
			"An environment variable wins on every start exactly as a flag does, so a field it "+
			"sets cannot be changed from the panel either", got["subdomain"])
	}
}

// Empty is not set. An exported-but-blank variable overrides nothing, and treating it as an
// override would disable a field for no reason.
func TestABlankEnvironmentVariableClaimsNothing(t *testing.T) {
	clearLaunchEnv(t)
	t.Setenv("LFT_CLIENT_SUBDOMAIN", "   ")

	if got := launchOverrides(); got["subdomain"] != "" {
		t.Errorf("a blank LFT_CLIENT_SUBDOMAIN claimed the field as %q", got["subdomain"])
	}
}

// The deprecated spellings still override, because they still work (#2061). A user running
// -server is exactly as overridden as one running -pin.
func TestDeprecatedFlagSpellingsStillCount(t *testing.T) {
	for _, name := range []string{"pin", "server", "bootstrap", "gateway"} {
		found := false
		for _, source := range settingSources {
			if source.Key != "server_url" {
				continue
			}
			for _, f := range source.Flags {
				if f == name {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("-%s does not mark server_url as overridden, so a client started with it "+
				"shows an editable Server URL that the flag overrides at every start", name)
		}
	}
}

func TestDescribeIsStableAndNamesTheSource(t *testing.T) {
	got := describeLaunchOverrides(map[string]string{
		"subdomain": "-subdomain", "auth_token": "LFT_TOKEN",
	})
	want := "auth_token=LFT_TOKEN, subdomain=-subdomain"
	if got != want {
		t.Errorf("got %q, want %q (sorted, so a log line does not reorder between runs)", got, want)
	}
	if describeLaunchOverrides(nil) != "" {
		t.Error("an empty map should render as nothing at all, not as an empty list")
	}
}
