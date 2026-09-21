package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"
)

// controlPlanePathPrefixes are the API groups whose data lives only in central's database.
//
// Deliberately prefixes rather than a list of routes: a route added under one of these later is
// covered the day it is written, which is the failure mode this is guarding. A test enumerates
// the literal routes out of ServeHTTP and checks each one against this, so a group that grows a
// path this does not cover fails the build rather than inheriting the old misleading answer.
var controlPlanePathPrefixes = []string{
	"/api/portal/",
	"/api/admin/",
}

// isControlPlanePath reports whether a path can only be answered by the control plane.
func isControlPlanePath(path string) bool {
	for _, prefix := range controlPlanePathPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// holdsNoControlPlaneData reports whether this gateway can answer a control-plane request at all.
//
// Deliberately NOT isEdgeNode(), which this package keeps as the single answer to "is this an
// edge" and which additionally requires EdgeToken. The question here is narrower and is about
// capability rather than role: a node with no database cannot answer, whether or not it is a
// correctly configured edge. A misconfigured edge missing its token would otherwise go on
// returning the misleading 401 this exists to remove.
//
// It is also the exact condition validatePAT tests before refusing a token unread, so the two
// cannot drift into disagreeing about which requests are answerable.
func (s *Server) holdsNoControlPlaneData() bool {
	return s.db == nil
}

// respondMisdirected answers a control-plane request that reached an edge.
//
// 421 Misdirected Request is exactly this situation: a request sent to a server unable to produce
// a response for it. The body names where the request should have gone, because an error that
// says only "not here" leaves the reader to guess -- and the address is one the edge already
// knows.
func (s *Server) respondMisdirected(w http.ResponseWriter, r *http.Request) {
	controlPlane := s.getPortalBaseURL(r)

	w.Header().Set("Content-Type", "application/json")
	// Advertised so a caller can act on it without parsing prose.
	w.Header().Set("X-Control-Plane", controlPlane)
	w.WriteHeader(http.StatusMisdirectedRequest)

	// Not "unauthorized": the credential was never examined, and saying so is the whole point.
	if _, err := fmt.Fprintf(w,
		`{"error":"This gateway is a regional edge and does not hold the data for %s. `+
			`Send control-plane requests to the control plane. Your credentials were not checked `+
			`and are not the problem.","control_plane":%q}`+"\n",
		jsonEscape(r.URL.Path), controlPlane); err != nil {
		// The client has gone. Nothing to recover, but a silent write failure on the one
		// response whose job is to explain itself is worth a line.
		slog.Info(fmt.Sprintf("[Edge] Could not write the misdirected-request reply for %s: %v",
			r.URL.Path, err))
	}
}

// jsonEscape makes a path safe to embed in the message above. The path is attacker-controlled,
// and a quote in it would otherwise break the JSON this hand-writes.
func jsonEscape(s string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", "", "\r", "")
	return replacer.Replace(s)
}
