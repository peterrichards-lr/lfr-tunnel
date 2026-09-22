package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
)

// A setting that was given on the command line or in the environment is set there on EVERY
// start, including the restart the Settings panel offers (#2088).
//
// That matters more than it looks. The restart deliberately reuses the same argv, so a client
// started with `-subdomain peters` comes back as `peters` however many times the panel saves
// something else. Editing such a field is not "applies at next start" -- it is "never applies
// while that flag is used", and a panel that offers an editable box is lying about it.
//
// So the panel needs to know which fields are spoken for before it renders them, and this is
// where that is decided. flag.Visit reports only flags actually SET, which is the distinction
// that matters: a flag left at its default has not taken the setting over.

// keySubdomain is named because goconst counts it across this file and main.go, and a typo in
// one of them would silently stop a field being detected as claimed.
const keySubdomain = "subdomain"

// settingSource names one config key and the places that can claim it.
type settingSource struct {
	// Key is the field name the Settings panel and /api/config use.
	Key string
	// Flags are the command-line spellings, newest first. Deprecated aliases are included:
	// a user running the old name is just as overridden as one running the new (#2061).
	Flags []string
	// EnvVars are the environment variables read for this key.
	EnvVars []string
}

// settingSources covers exactly the fields the Settings panel offers. Anything not listed is
// simply never reported as overridden, which is the safe direction: a missing entry makes a
// field editable, and the worst case is the existing behaviour.
//
// TestEveryNamedFlagExists keeps the Flags column honest -- a renamed flag that stopped being
// detected here would silently re-open the bug this file exists to close.
var settingSources = []settingSource{
	{Key: "server_url", Flags: []string{"pin", "server", "bootstrap", "gateway"},
		EnvVars: []string{"LFT_CLIENT_SERVER", "LFT_SERVER_URL", "LFT_SERVER"}},
	{Key: "auth_token", Flags: []string{"token"},
		EnvVars: []string{"LFT_CLIENT_TOKEN", "LFT_TOKEN"}},
	{Key: "target_host", Flags: []string{"target-host"},
		EnvVars: []string{"LFT_TARGET_HOST"}},
	{Key: "dest_port", Flags: []string{"ports"},
		EnvVars: []string{"LFT_CLIENT_PORTS"}},
	{Key: keySubdomain, Flags: []string{keySubdomain},
		EnvVars: []string{"LFT_CLIENT_SUBDOMAIN", "LFT_SUBDOMAIN"}},
	{Key: "preserve_host", Flags: []string{"preserve-host"}},
	{Key: "insecure_skip_verify", Flags: []string{"insecure-skip-verify"},
		EnvVars: []string{"LFT_INSECURE_SKIP_VERIFY"}},
	{Key: "log_dir", Flags: []string{"log-dir"}},
}

// launchOverrides reports, per config key, what claimed it at launch -- "-subdomain" or
// "LFT_SUBDOMAIN" -- for the keys where something did. Empty map means nothing is spoken for.
func launchOverrides() map[string]string {
	set := make(map[string]bool)
	flag.Visit(func(f *flag.Flag) { set[f.Name] = true })

	out := make(map[string]string)
	for _, s := range settingSources {
		for _, name := range s.Flags {
			if set[name] {
				out[s.Key] = "-" + name
				break
			}
		}
		if _, claimed := out[s.Key]; claimed {
			continue
		}
		for _, env := range s.EnvVars {
			if strings.TrimSpace(os.Getenv(env)) != "" {
				out[s.Key] = env
				break
			}
		}
	}
	return out
}

// describeLaunchOverrides renders the map for a log line, in a stable order.
func describeLaunchOverrides(overrides map[string]string) string {
	if len(overrides) == 0 {
		return ""
	}
	keys := make([]string, 0, len(overrides))
	for k := range overrides {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", k, overrides[k]))
	}
	return strings.Join(parts, ", ")
}

// launchFlagNames reports the NAMES of the flags actually given on the command line, sorted.
//
// Names, never values. `-passcode`, `-basic-auth` and `-token` all arrive as flags, so a record
// carrying values would be a credential leak of exactly the kind #2135 and #2137 just closed. A
// flag's name is not a secret; what was passed to it is.
//
// flag.Visit walks only the flags that were SET, which is the whole point: "-prefer-region was
// given" and "no routing flag was given" are different answers, and the second is what tells you
// a client's region came from a latency probe.
//
// Reported alongside launchOverrides rather than folded into it: that map answers "what claimed
// this config key", including env vars, and is what the Inspector locks fields on. This answers
// "how was the process started", which includes flags that are not config settings at all --
// -gui, -background, -prefer-region.
func launchFlagNames() []string {
	var names []string
	flag.Visit(func(f *flag.Flag) { names = append(names, f.Name) })
	sort.Strings(names)
	return names
}
