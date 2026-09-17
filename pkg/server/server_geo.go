package server

import (
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"

	"lfr-tunnel/pkg/db"
	"lfr-tunnel/pkg/geo"
)

// geoStore adapts *db.DB to geo.Store.
//
// The two structs are identical, and the conversion exists only to keep pkg/db a leaf
// package: nothing else under pkg/db imports another pkg/, and a analytics feature is a
// poor reason to be the first.
type geoStore struct {
	database *db.DB
}

func (s geoStore) UpsertLocationStats(period string, counts []geo.BucketCount) error {
	stats := make([]db.LocationStat, 0, len(counts))
	for _, c := range counts {
		stats = append(stats, db.LocationStat{Bucket: c.Bucket, Count: c.Count})
	}
	return s.database.UpsertLocationStats(period, stats)
}

// geoReason names why the aggregator is nil (#1938).
//
// Three distinct operator situations used to collapse to one nil and one sentence in the
// panel -- "no geo-IP database is configured" -- which told an operator who had configured
// one, and mistyped it, that they had configured nothing. The reason is retained so the
// panel can say which of them happened; the registration path still branches on the nil
// aggregator alone and is unchanged by any of this.
type geoReason string

const (
	// geoReasonNotConfigured is the default and is not a fault: no database ships with
	// the gateway and none has to.
	geoReasonNotConfigured geoReason = "not_configured"
	// geoReasonPathNotFound is a configured path with no file at it -- a typo, the wrong
	// directory, or a location a systemd sandbox hides.
	geoReasonPathNotFound geoReason = "path_not_found"
	// geoReasonUnreadable is a file that exists and cannot be used: the wrong format (an
	// IP2Location .BIN says so in Detail), a truncated download, or permissions.
	geoReasonUnreadable geoReason = "unreadable"
	// geoReasonProviderNotDeclared is a database that is present and readable, with no
	// country_db_provider naming who published it (#1964).
	//
	// A distinct reason rather than folding into not_configured, because the operator HAS
	// configured a database and telling them they have not would send them to check the
	// path -- the same mistake #1938 fixed for the other two situations.
	geoReasonProviderNotDeclared geoReason = "provider_not_declared"
	// geoReasonProviderUnknown is a declared vendor this build does not recognise, i.e. a
	// typo. Separated from the above so the message can quote what was actually set.
	geoReasonProviderUnknown geoReason = "provider_unknown"
)

// geoDiagnosis is why the feature is off, kept for the admin panel.
//
// Path and Detail are ADMIN-ONLY diagnostic detail: they name a filesystem path on the
// gateway host. They leave the process through handleGetLocationAnalytics and nothing
// else, and that route is dispatched from handleAdminEndpoints, which calls requireAdmin
// before any of it runs.
type geoDiagnosis struct {
	Reason geoReason
	Path   string
	Detail string
}

// newGeoAggregator builds the anonymous geographic aggregator, or returns nil plus the
// reason when the deployment has no usable geo-IP database (#1152, #1938).
//
// nil is a working no-op rather than an error, deliberately: this sits on the
// registration path, and geo-IP being unconfigured must never be able to stop a user
// connecting or a server starting. That stays true for every reason below -- an
// unreadable file disables the feature exactly as an absent one does.
//
// bothPathsSet comes from config.CountryDatabasePath: `country_db_path` and its
// `geolite2_db_path` alias both name a file, and they disagree. The neutral key wins (see
// that function for why), but the loser is not dropped in silence -- an operator who
// migrated the key and left the old line behind would otherwise have no way to learn which
// file is open except by reading source (#1921).
func newGeoAggregator(path, declaredProvider string, bothPathsSet bool, database *db.DB) (*geo.Aggregator, geoDiagnosis) {
	if database == nil {
		// Unreachable in production -- NewServer has a database open before it gets here --
		// but tests construct servers directly, and "no store" is genuinely "off", not an
		// operator mistake to report against their path.
		return nil, geoDiagnosis{Reason: geoReasonNotConfigured}
	}
	if bothPathsSet {
		slog.Warn("[Geo] Both country_db_path and geolite2_db_path are set; the neutral key wins "+
			"and the alias is ignored. Remove geolite2_db_path.", "using", path)
	}
	// The vendor in force at startup, which is the DB row when the portal has set one and the
	// YAML key otherwise (#1995). Used here for the log line and for the metadata-mismatch
	// warning only: it is NOT what decides whether the aggregator gets built, because it is
	// now changeable while the process runs and the aggregator is not.
	inForce, _ := geoProviderInForce(database, declaredProvider)
	declared, perr := geo.ParseProvider(inForce)
	// Said at boot as well as in the panel, and only when something WAS set: an unset vendor
	// is the honest default and warning about it would fire on every deployment that has not
	// made the choice yet -- training the reader to ignore the line that matters when they
	// have made it, and made it wrongly. A value that does not parse is the opposite case:
	// somebody typed a vendor, and the panel will credit nobody until it is corrected.
	if perr != nil && inForce != "" {
		slog.Warn("[Geo] The vendor in force is not one this build knows, so the panel will "+
			"publish no credit and no rows until it is corrected in System Settings.",
			"country_db_provider", inForce, "error", perr)
	}

	resolver, err := geo.OpenResolver(path, declared)
	if err != nil {
		switch {
		case errors.Is(err, geo.ErrNotFound):
			// Configured and missing. Warned rather than silent, and now also reported in
			// the panel: the journal was the only discriminator, and it scrolls away.
			slog.Warn("[Geo] Geo-IP database not found; geographic distribution disabled", "path", path)
			return nil, geoDiagnosis{Reason: geoReasonPathNotFound, Path: path}
		case errors.Is(err, geo.ErrUnavailable):
			// Not configured: the default, and silent on purpose.
			return nil, geoDiagnosis{Reason: geoReasonNotConfigured}
		}
		slog.Warn("[Geo] Failed to open geo-IP database; geographic distribution disabled", "path", path, "error", err)
		// err carries the .BIN-vs-.mmdb diagnosis from #1921, which is the single most
		// useful sentence an operator in this state can be shown.
		return nil, geoDiagnosis{Reason: geoReasonUnreadable, Path: path, Detail: err.Error()}
	}

	// The database is present and readable, so the aggregator is built. The VENDOR gate is
	// applied by handleGetLocationAnalytics instead, once per request (#1995).
	//
	// It used to be applied here, which meant an operator who named the vendor after the
	// gateway started was told to restart it -- and the vendor is the one part of this that
	// genuinely does not need a restart. It never touches decoding: Country() resolves through
	// countryPaths whichever vendor supplied the file, and the provider is read in exactly two
	// places, this log line and the attribution the panel renders. Refusing to OPEN a perfectly
	// readable file over a display value was the wrong lever.
	//
	// The protection itself is unchanged and must stay that way: with no vendor in force the
	// panel renders the "choose a vendor" state and no rows, because every supported vendor's
	// licence obliges a DIFFERENT visible credit and guessing is a licence breach rather than a
	// cosmetic error (#1964). What moved is WHERE that is decided, not WHETHER.
	slog.Info("[Geo] Anonymous geographic distribution enabled",
		"path", path, "provider", string(declared), "threshold", geo.DefaultThreshold)
	// Path is retained on the success diagnosis too, so System Settings can show the operator
	// which file is actually open. It is admin-only, like the rest of this struct.
	return geo.New(resolver, geoStore{database: database}, geo.Options{}), geoDiagnosis{Path: path}
}

// observeGeoLocation records one registration against its country.
//
// clientIP is resolved to a country in memory by the aggregator and then dropped; it is
// neither stored nor logged here. Anything unparseable is ignored -- there is nothing a
// caller could usefully do about it, and this must not add a failure mode to
// registration.
func (s *Server) observeGeoLocation(userID, clientIP string) {
	if s == nil || s.geo == nil || userID == "" || clientIP == "" {
		return
	}
	// clientIPFrom yields a bare address, but an edge node forwards whatever it resolved,
	// so tolerate a host:port form too.
	raw := strings.TrimSpace(clientIP)
	addr, err := netip.ParseAddr(raw)
	if err != nil {
		ap, perr := netip.ParseAddrPort(raw)
		if perr != nil {
			return
		}
		addr = ap.Addr()
	}
	s.geo.Observe(userID, addr)
}

// apiError is the error body these handlers return.
//
// A typed struct rather than map[string]string{"error": ...}, because goconst counts the
// bare "error" key at 88 occurrences repo-wide. The 49 in server.go do not get reported
// only because that file is on .golangci.yml's per-file exclusion list -- and the comment
// above that list says plainly that listing files exists so a NEW file in these packages
// is linted from birth. This is a new file, so it is linted, and the right answer is to
// not add the 89th rather than to add an exclusion (#319).
//
// The JSON is byte-identical to the map form.
type apiError struct {
	Error string `json:"error"`
}

// locationAnalyticsResponse is the payload of GET /api/admin/analytics/locations.
//
// Available distinguishes "no geo-IP database deployed" from "deployed, but nothing has
// cleared the k-threshold yet". They look identical in the data and mean very different
// things to an admin looking at an empty panel.
//
// Reason splits that first state three ways (#1938): unset, configured-but-missing and
// configured-but-unreadable were one value here, so the panel told an operator who had
// mistyped the path that they had never set one. Available keeps its meaning exactly, so
// no existing client changes behaviour -- the new fields are additive and omitted when the
// feature is on.
type locationAnalyticsResponse struct {
	Available bool `json:"available"`
	// Provider is the vendor in force -- "maxmind", "dbip" or "ip2location" (#1921, #1995).
	//
	// Declared, never derived: an IP2Location MMDB reports MaxMind's own database_type,
	// description, languages and record schema, so deriving it credited MaxMind for
	// IP2Location's data (#1964). Resolved per request from the portal row or the YAML key,
	// so switching vendor takes effect without a restart.
	//
	// A key, not a rendered sentence: the credit each vendor's licence obliges is a
	// translatable string that belongs in the i18n bundles with every other portal string,
	// and both portal arms already resolve keys through the same bundle. Sending prose from
	// here would put one more string outside `make check-i18n`'s reach.
	//
	// Empty when Available is false -- with no database open there is no data on screen and
	// so nothing to attribute.
	Provider string `json:"provider,omitempty"`
	// Reason is one of the geoReason constants, and is empty when Available.
	Reason geoReason `json:"reason,omitempty"`
	// ConfiguredPath is the country_db_path (or its geolite2_db_path alias) the gateway
	// actually tried, sent only for the two states the operator has to correct so the typo
	// is visible where the complaint is. Admin-only, like the whole route -- see
	// geoDiagnosis.
	ConfiguredPath string `json:"configured_path,omitempty"`
	// Detail is the underlying open error, which for an IP2Location .BIN names the wrong
	// download rather than the wrong path (#1921). Present only for geoReasonUnreadable.
	Detail    string            `json:"detail,omitempty"`
	Period    string            `json:"period"`
	Threshold int               `json:"threshold"`
	Buckets   []db.LocationStat `json:"buckets"`
}

// handleGetLocationAnalytics serves the anonymous geographic distribution for the most
// recent ISO week on record.
//
// Admin-only by virtue of its route: it is dispatched from handleAdminEndpoints, which
// calls requireAdmin once at the top.
func (s *Server) handleGetLocationAnalytics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
		return
	}
	// The vendor in force RIGHT NOW, not the one that was in force at startup (#1995). A DB
	// row set from System Settings wins; with no row the YAML key applies. Resolved per
	// request because it is a display value -- nothing about decoding depends on it -- so
	// changing it must not require a restart.
	provider, _, provErr := s.effectiveGeoProvider()
	resp := locationAnalyticsResponse{
		// Both halves. The file has to be open AND a vendor has to be named: rendering rows
		// under no credit at all is the licence breach this gate exists to prevent, and
		// rendering a credit with no database open would attribute data nobody is looking at.
		Available: s.geo != nil && provErr == nil,
		Threshold: geo.DefaultThreshold,
		Buckets:   []db.LocationStat{},
	}
	switch {
	case resp.Available:
		// Which vendor's credit the panel must render (#1921).
		resp.Provider = string(provider)
	case s.geo != nil:
		// A readable database with no usable vendor. Before #1995 this state was decided at
		// startup and the aggregator was never built; the reasons and the wording they drive
		// are deliberately the same two, so both portals' existing messages still apply.
		resp.ConfiguredPath = s.geoDiagnosis.Path
		if errors.Is(provErr, geo.ErrProviderNotDeclared) {
			resp.Reason = geoReasonProviderNotDeclared
		} else {
			resp.Reason = geoReasonProviderUnknown
			resp.Detail = provErr.Error()
		}
	default:
		// Why it is off, not just that it is (#1938). Read from what the constructor
		// recorded at startup rather than re-stat'ing the path here: this must describe the
		// state the running process is actually in, and a file created since startup is not
		// open and would make the panel claim a feature the gateway is not running.
		resp.Reason = s.geoDiagnosis.Reason
		if resp.Reason == "" {
			resp.Reason = geoReasonNotConfigured
		}
		resp.ConfiguredPath = s.geoDiagnosis.Path
		resp.Detail = s.geoDiagnosis.Detail
	}
	if !resp.Available || s.db == nil {
		// No rows unless a vendor is in force (#1995). The aggregator now runs whenever the
		// file opens, so counts can exist for a period before anybody named the publisher --
		// and serving them would put a vendor's results on an admin's screen with that
		// vendor's required credit nowhere, which is the licence breach this whole gate
		// exists to prevent. They are not discarded, only withheld: naming the vendor makes
		// the history that was already collected visible, rather than starting from zero.
		respondJSON(w, http.StatusOK, resp)
		return
	}
	// Read what is stored rather than what is in memory. The in-flight set of users for
	// the current period is never exposed by any route, which is why the aggregator has
	// no method that could serve one.
	period, stats, err := s.db.GetLocationStats("")
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, apiError{Error: "failed to read location stats"})
		return
	}
	resp.Period = period
	if stats != nil {
		resp.Buckets = stats
	}
	respondJSON(w, http.StatusOK, resp)
}
