package config

import "testing"

// The geo-IP database path has two accepted spellings (#1921): the provider-neutral
// `country_db_path`, and `geolite2_db_path`, the MaxMind-named original that a live gateway
// is still configured with.
//
// These tests go through LoadServerConfig rather than calling CountryDatabasePath on a
// hand-built struct, because the YAML decode is half of what is being claimed -- a key with
// the wrong tag would satisfy a struct-level test and load as empty in production.
// writeServerConfig is shared with config_admin_email_test.go.

// TestTheLegacyGeoKeyStillResolves is the compatibility requirement, and it is not
// hypothetical: the production central gateway's config uses this spelling. A rename that
// switches the feature off on upgrade is not an improvement.
func TestTheLegacyGeoKeyStillResolves(t *testing.T) {
	cfg, err := LoadServerConfig(writeServerConfig(t, "geolite2_db_path: \"/etc/lfr-tunneld/geoip/country.mmdb\"\n"))
	if err != nil {
		t.Fatalf("LoadServerConfig: %v", err)
	}
	path, both := cfg.CountryDatabasePath()
	if path != "/etc/lfr-tunneld/geoip/country.mmdb" {
		t.Errorf("the old spelling resolved to %q -- an existing deployment would silently lose the feature", path)
	}
	if both {
		t.Errorf("only one key is set; nothing should be reported as conflicting")
	}
}

// TestTheNeutralGeoKeyResolves covers the new spelling on its own, which is what a fresh
// deployment will write.
func TestTheNeutralGeoKeyResolves(t *testing.T) {
	cfg, err := LoadServerConfig(writeServerConfig(t, "country_db_path: \"/srv/geoip/dbip-country-lite.mmdb\"\n"))
	if err != nil {
		t.Fatalf("LoadServerConfig: %v", err)
	}
	path, both := cfg.CountryDatabasePath()
	if path != "/srv/geoip/dbip-country-lite.mmdb" {
		t.Errorf("the neutral key resolved to %q, want the path it names", path)
	}
	if both {
		t.Errorf("only one key is set; nothing should be reported as conflicting")
	}
}

// TestTheNeutralGeoKeyBeatsTheAlias pins the precedence, and the direction matters.
//
// If the alias won, adding `country_db_path` to a config that still carries
// `geolite2_db_path` would do nothing whatsoever -- silently, with a startup log naming the
// old path and looking entirely healthy. That is the one outcome an operator cannot diagnose
// from outside the process. The reverse mistake announces itself in the same log line.
func TestTheNeutralGeoKeyBeatsTheAlias(t *testing.T) {
	cfg, err := LoadServerConfig(writeServerConfig(t,
		"country_db_path: \"/srv/geoip/new.mmdb\"\ngeolite2_db_path: \"/srv/geoip/old.mmdb\"\n"))
	if err != nil {
		t.Fatalf("LoadServerConfig: %v", err)
	}
	path, both := cfg.CountryDatabasePath()
	if path != "/srv/geoip/new.mmdb" {
		t.Errorf("both keys set: resolved to %q, want the neutral key's value", path)
	}
	if !both {
		t.Errorf("both keys name a different file and that was not reported -- the operator " +
			"would never learn which one is open")
	}
}

// TestTwoSpellingsOfTheSamePathIsNotAConflict. Someone migrating a config will reasonably
// write both lines pointing at the same file; warning about that would train them to ignore
// the warning that matters.
func TestTwoSpellingsOfTheSamePathIsNotAConflict(t *testing.T) {
	cfg, err := LoadServerConfig(writeServerConfig(t,
		"country_db_path: \"/srv/geoip/country.mmdb\"\ngeolite2_db_path: \"/srv/geoip/country.mmdb\"\n"))
	if err != nil {
		t.Fatalf("LoadServerConfig: %v", err)
	}
	if path, both := cfg.CountryDatabasePath(); both {
		t.Errorf("identical paths under both spellings reported as a conflict (path %q)", path)
	}
}

// TestNoGeoKeyIsStillASupportedConfiguration. Shipping no database is the default and the
// state most deployments are in permanently; it must not become an error or a non-empty path.
func TestNoGeoKeyIsStillASupportedConfiguration(t *testing.T) {
	cfg, err := LoadServerConfig(writeServerConfig(t, "domains:\n  - example.com\n"))
	if err != nil {
		t.Fatalf("LoadServerConfig: %v", err)
	}
	if path, both := cfg.CountryDatabasePath(); path != "" || both {
		t.Errorf("no geo key set resolved to (%q, %v), want (\"\", false)", path, both)
	}
}

// TestBothGeoEnvironmentOverridesAreHonoured. `LFT_GEOLITE2_DB_PATH` is documented and in
// use; the neutral key needs its own or a container deployment that adopted the new spelling
// would have no environment override at all.
func TestBothGeoEnvironmentOverridesAreHonoured(t *testing.T) {
	yaml := writeServerConfig(t, "geolite2_db_path: \"/from/yaml.mmdb\"\n")

	t.Setenv("LFT_GEOLITE2_DB_PATH", "/from/legacy/env.mmdb")
	cfg, err := LoadServerConfig(yaml)
	if err != nil {
		t.Fatalf("LoadServerConfig: %v", err)
	}
	if path, _ := cfg.CountryDatabasePath(); path != "/from/legacy/env.mmdb" {
		t.Errorf("LFT_GEOLITE2_DB_PATH gave %q, want it to override the YAML value", path)
	}

	t.Setenv("LFT_COUNTRY_DB_PATH", "/from/neutral/env.mmdb")
	cfg, err = LoadServerConfig(yaml)
	if err != nil {
		t.Fatalf("LoadServerConfig: %v", err)
	}
	if path, _ := cfg.CountryDatabasePath(); path != "/from/neutral/env.mmdb" {
		t.Errorf("LFT_COUNTRY_DB_PATH gave %q, want the neutral override to win", path)
	}
}

// TestASurroundingSpaceInTheGeoPathIsTrimmed. The value is handed straight to os.Stat, and a
// trailing space in a YAML scalar is invisible in an editor but turns the path into a
// different, missing file -- which reports as "not configured" in the panel (#1938).
func TestASurroundingSpaceInTheGeoPathIsTrimmed(t *testing.T) {
	cfg, err := LoadServerConfig(writeServerConfig(t, "country_db_path: \"  /srv/geoip/country.mmdb  \"\n"))
	if err != nil {
		t.Fatalf("LoadServerConfig: %v", err)
	}
	if path, _ := cfg.CountryDatabasePath(); path != "/srv/geoip/country.mmdb" {
		t.Errorf("padded path resolved to %q, want it trimmed", path)
	}
}
