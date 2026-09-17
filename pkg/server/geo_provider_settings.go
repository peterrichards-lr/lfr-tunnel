package server

import (
	"strings"

	"lfr-tunnel/pkg/db"
	"lfr-tunnel/pkg/geo"
)

// The geo-IP vendor, choosable from System Settings (#1995).
//
// #1990 made country_db_provider a required server-config key, because the vendor cannot be
// derived: a real IP2Location MMDB reports MaxMind's own database_type, description, languages
// AND record schema, so deriving it credited MaxMind for IP2Location's data and left
// IP2Location's own prescribed acknowledgment unshown. Each vendor's licence obliges a
// DIFFERENT visible credit, so a wrong value is a licence problem rather than a cosmetic one.
//
// Typing that value into YAML is the part a person can still get wrong, and nothing catches it
// except the operator noticing the panel is off. So it is chosen from a list instead, and the
// list comes from the gateway (geo.SelectableProviders) rather than from a copy held in each
// portal -- the #1882 lesson, where the portal's own copy of the alert vocabulary left three
// alerts with no control at all.
//
// This is cheap because the provider NEVER touches decoding. geo.Resolver.Country() resolves
// through countryPaths whichever vendor supplied the file; the provider is read in exactly two
// places, one startup log line and the attribution the panel renders. Changing it needs no
// restart, no reopening of the database and no rebuild of the aggregator -- unlike
// country_db_path, which names a file that has to be opened at startup and which therefore
// stays YAML-only.

// geoProviderSettingKey is the admin_settings row the portal writes.
//
// Deliberately spelled the same as the YAML key. The two are the two halves of one setting,
// and giving the stored one its own name would make the precedence rule below harder to state
// than it is.
const geoProviderSettingKey = "country_db_provider"

// geoProviderSource names WHICH of the two configuration sources supplied the vendor in force.
//
// Sent to the portal and rendered, not merely computed. The failure this repo keeps hitting is
// two sources of truth disagreeing silently (#1412, #1921, #1919): without this an operator
// edits server-config.yaml, sees no change, and has no way to learn that a portal row is
// overriding them.
type geoProviderSource string

const (
	// geoProviderSourceUnset is no vendor anywhere -- the honest default, and the state in
	// which the panel shows "choose a vendor" rather than rows.
	geoProviderSourceUnset geoProviderSource = ""
	// geoProviderSourcePortal is an admin_settings row, set from System Settings. It wins.
	geoProviderSourcePortal geoProviderSource = "portal"
	// geoProviderSourceServerConfig is country_db_provider in server-config.yaml, which
	// applies whenever there is no portal row.
	geoProviderSourceServerConfig geoProviderSource = "server_config"
)

// GeoProviderOption is one selectable vendor as the portals receive it.
//
// The i18n KEYS travel, never the rendered prose. Both portal arms already resolve keys through
// the same bundle, and the credit lines in particular are licence text that belongs in
// pkg/server/i18n with every other portal string -- sending the sentence from here would put it
// outside `make check-i18n`'s reach, which is how 477 keys drifted out before #1701.
type GeoProviderOption struct {
	// Value is what is stored and what the portals POST back: geo.Provider's own spelling.
	Value string `json:"value"`
	// LabelKey is the i18n key for the dropdown entry's label.
	LabelKey string `json:"label_key"`
	// AttributionKey is the i18n key for the credit that vendor's licence obliges, which is
	// the SAME key the geographic panel renders. One key, so the preview in System Settings
	// cannot show an admin something different from what gets published.
	AttributionKey string `json:"attribution_key"`
}

// GeoProviderOptions is the dropdown's vocabulary, derived from geo.SelectableProviders().
//
// Derived, not listed. A second list here would be free to disagree with the one ParseProvider
// validates against, and the two ways it can disagree are both silent: an option offered but
// 400'd on save, or a vendor accepted on save that the dropdown never offers.
//
// The "(not set)" entry is NOT in here. It is the absence of a choice rather than a vendor, it
// has no label of its own to translate per-vendor and no credit to render, and folding it in
// would make every consumer special-case one member of its own vocabulary.
func GeoProviderOptions() []GeoProviderOption {
	providers := geo.SelectableProviders()
	out := make([]GeoProviderOption, 0, len(providers))
	for _, p := range providers {
		out = append(out, GeoProviderOption{
			Value: string(p),
			// Derived from the value so a vendor added to geo.SelectableProviders cannot
			// arrive without keys; check-i18n-keys then requires both in every bundle,
			// and TestEveryOfferedVendorHasALabelAndACredit requires them in English.
			LabelKey:       "geo_provider_" + string(p),
			AttributionKey: "geo_attribution_" + string(p),
		})
	}
	return out
}

// geoProviderInForce applies the precedence rule: a portal row wins, and the YAML key applies
// when there is none.
//
// Returns the RAW declared value rather than a parsed one, so the caller can tell an unset
// vendor from a misspelled one -- the two are different operator situations with different
// remedies, which is why they have stayed separate reasons on the analytics response since
// #1964. A value the build does not recognise still reports the source that supplied it:
// saying a bad portal row came from server-config.yaml would send someone to edit a file that
// is not the problem.
//
// A package-level function taking the store rather than a method, because newGeoAggregator
// needs it before the Server exists -- and the alternative, letting startup read only the YAML
// value, would make the boot log name a vendor different from the one the panel credits.
//
// An unreadable row falls through to the YAML value rather than failing: the fallback is the
// operator's own configured value, not a guess, and refusing to resolve at all would take the
// panel off over a transient database error.
func geoProviderInForce(database *db.DB, configured string) (raw string, source geoProviderSource) {
	if database != nil {
		if stored, err := database.GetAdminSetting(geoProviderSettingKey); err == nil {
			if stored = strings.TrimSpace(stored); stored != "" {
				return stored, geoProviderSourcePortal
			}
		}
	}
	if configured = strings.TrimSpace(configured); configured != "" {
		return configured, geoProviderSourceServerConfig
	}
	return "", geoProviderSourceUnset
}

// effectiveGeoProvider is the vendor in force, where it came from, and why not if it is not.
//
// The error is geo.ErrProviderNotDeclared when nothing is set anywhere and geo.ErrProviderUnknown
// when what IS set names a vendor this build does not know.
func (s *Server) effectiveGeoProvider() (geo.Provider, geoProviderSource, error) {
	if s == nil {
		return geo.ProviderUnknown, geoProviderSourceUnset, geo.ErrProviderNotDeclared
	}
	// The YAML half comes from the source the LAST LOAD applied, not from the config this
	// process started with (#1995 + #1998). SIGHUP can swap the database and its declared
	// vendor without restarting, and it does not write back to s.cfg -- so reading s.cfg here
	// would credit the vendor of a file that was replaced minutes ago, which is exactly the
	// false attribution #1964 exists to prevent. geoSource is set in NewServer and updated by
	// every successful reload, so it is the same value at startup and current afterwards.
	s.geoMu.RLock()
	source := s.geoSource
	s.geoMu.RUnlock()
	configured := source.Provider
	if source == (geoSource{}) && s.cfg != nil {
		// A Server built as a struct literal in a test never went through NewServer and so has
		// no geoSource at all. Told apart from a deliberately CLEARED vendor by comparing the
		// whole source, not just the provider: an operator who deletes country_db_provider has
		// set it to nothing on purpose, and falling back to the startup config there would keep
		// crediting a vendor they just withdrew.
		configured = s.cfg.CountryDBProvider
	}
	raw, providerSource := geoProviderInForce(s.db, configured)
	p, err := geo.ParseProvider(raw)
	return p, providerSource, err
}

// geoProviderIsSelectable reports whether value is one the dropdown offers.
//
// The empty string is accepted by the settings endpoint but is NOT selectable in this sense:
// it clears the portal row rather than naming a vendor, so the caller handles it separately.
func geoProviderIsSelectable(value string) bool {
	for _, p := range geo.SelectableProviders() {
		if value == string(p) {
			return true
		}
	}
	return false
}

// addGeoProviderSettings writes the geo-IP vendor block onto the System Settings response
// (#1995).
//
// Five fields rather than one, because "which vendor" and "where that came from" are separate
// questions and the screen has to answer both. An operator who edits server-config.yaml and
// sees no change must be able to learn, on this screen, that a portal row is overriding them --
// the silent-disagreement failure #1412, #1921 and #1919 all ran into.
//
//	geo_providers               the vocabulary this build supports, so the portal renders what
//	                            the gateway declares rather than its own copy of the list
//	country_db_provider         the vendor IN FORCE, or "" when none is usable
//	country_db_provider_source  which source supplied it: "portal", "server_config", or ""
//	country_db_provider_config  the raw server-config.yaml value, so the screen can show what
//	                            is being overridden -- and can name a typo that produced ""
//	country_db_path             the database file, READ-ONLY. It stays YAML-only: it is a
//	                            filesystem path read at startup, and letting an admin session
//	                            point the gateway at an arbitrary readable path is a
//	                            file-disclosure vector. Showing it is useful; setting it is not
//	                            worth the hole.
func (s *Server) addGeoProviderSettings(out map[string]interface{}) {
	out["geo_providers"] = GeoProviderOptions()

	provider, source, err := s.effectiveGeoProvider()
	inForce := ""
	if err == nil {
		inForce = string(provider)
	}
	out["country_db_provider"] = inForce
	out["country_db_provider_source"] = string(source)

	if s.cfg != nil {
		out["country_db_provider_config"] = strings.TrimSpace(s.cfg.CountryDBProvider)
		path, _ := s.cfg.CountryDatabasePath()
		out["country_db_path"] = path
	} else {
		out["country_db_provider_config"] = ""
		out["country_db_path"] = ""
	}
}
