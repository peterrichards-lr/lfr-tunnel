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

// newGeoAggregator builds the anonymous geographic aggregator, or returns nil when the
// deployment has no MaxMind database (#1152).
//
// nil is a working no-op rather than an error, deliberately: this sits on the
// registration path, and geo-IP being unconfigured must never be able to stop a user
// connecting or a server starting.
func newGeoAggregator(path string, database *db.DB) *geo.Aggregator {
	if database == nil {
		return nil
	}
	resolver, err := geo.OpenResolver(path)
	if err != nil {
		if errors.Is(err, geo.ErrUnavailable) {
			// Not configured. Silent at info level would be worse -- an operator who set
			// the path and typo'd it deserves to see the feature is off.
			if path != "" {
				slog.Warn("[Geo] Geo-IP database not found; geographic distribution disabled", "path", path)
			}
			return nil
		}
		slog.Warn("[Geo] Failed to open geo-IP database; geographic distribution disabled", "path", path, "error", err)
		return nil
	}
	slog.Info("[Geo] Anonymous geographic distribution enabled", "path", path, "threshold", geo.DefaultThreshold)
	return geo.New(resolver, geoStore{database: database}, geo.Options{})
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

// locationAnalyticsResponse is the payload of GET /api/admin/analytics/locations.
//
// Available distinguishes "no geo-IP database deployed" from "deployed, but nothing has
// cleared the k-threshold yet". They look identical in the data and mean very different
// things to an admin looking at an empty panel.
type locationAnalyticsResponse struct {
	Available bool              `json:"available"`
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
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	resp := locationAnalyticsResponse{
		Available: s.geo != nil,
		Threshold: geo.DefaultThreshold,
		Buckets:   []db.LocationStat{},
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
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to read location stats"})
		return
	}
	resp.Period = period
	if stats != nil {
		resp.Buckets = stats
	}
	respondJSON(w, http.StatusOK, resp)
}
