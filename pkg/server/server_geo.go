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
func newGeoAggregator(path string, bothPathsSet bool, database *db.DB) (*geo.Aggregator, geoDiagnosis) {
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
	resolver, err := geo.OpenResolver(path)
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
	// The provider is logged because it decides which attribution the panel renders, and
	// it is DERIVED from the file rather than configured (#1921). "unknown" here is the
	// operator's only warning that their vendor's credit line is not being shown.
	slog.Info("[Geo] Anonymous geographic distribution enabled",
		"path", path, "provider", string(resolver.Provider()), "threshold", geo.DefaultThreshold)
	return geo.New(resolver, geoStore{database: database}, geo.Options{}), geoDiagnosis{}
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
	// Provider is the vendor derived from the open database's own metadata -- "maxmind",
	// "dbip", "ip2location" or "unknown" (#1921).
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
	resp := locationAnalyticsResponse{
		Available: s.geo != nil,
		Threshold: geo.DefaultThreshold,
		Buckets:   []db.LocationStat{},
	}
	if resp.Available {
		// Which vendor's credit the panel must render (#1921). Only when a database is
		// actually open: with nothing open there is no data on screen to attribute.
		resp.Provider = string(s.geo.Provider())
	} else {
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
	if s.db == nil {
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
