package server

// PortalV2Base is where the V2 single-page app is mounted (`BrowserRouter basename` in
// ui/src/App.tsx). The paths in PortalV1SectionToV2 are relative to it.
const PortalV2Base = "/portalv2"

// PortalV1SectionToV2 maps every Portal V1 section to the V2 path that shows the same thing
// (#2527, epic #2524).
//
// Keys are V1 section names: the `<id>` of each `#tab-<id>` panel in dashboard.html, which is
// also the `<section>` of V1's `/portal/<section>` and `/admin/<section>` routes (#1513).
// Values are V2 router paths, relative to PortalV2Base.
//
// It exists so the V1 -> V2 redirects (#2528) have one table to read rather than a list of
// their own. V1 keeps its own sections in the DOM and nowhere else, and V2 keeps its routes in
// App.tsx and dashboardSections.ts, so this table is the only place the two meet --
// TestPortalV1SectionToV2 holds it against all three files, both ways round: a V1 section
// with no entry fails, and so does an entry pointing at a V2 path that no route serves.
//
// Where V1 had a route of its own for something V2 puts on the dashboard (tokens,
// reservations, tunnels), the value is a dashboard section path, `/dashboard/<section>`.
//
// Admin-only V1 sections map to admin-only V2 pages. Neither portal shows those to a
// non-admin: V1's showTab falls back to the overview, V2's AdminRoute to /dashboard.
var PortalV1SectionToV2 = map[string]string{
	"overview":     "/dashboard",
	"tokens":       "/dashboard/tokens",
	"reservations": "/dashboard/reservations",
	"tunnels":      "/dashboard/tunnels",
	"account":      "/account",
	"analytics":    "/analytics",

	"users": "/admin/users",
	// V2 lists pending registrations as a tab of the Users page, which has no address of its
	// own, so this lands on the page and not on the tab.
	"registrations":    "/admin/users",
	"admin-subdomains": "/admin/subdomains",
	"blacklist":        "/admin/blacklist",
	"audit":            "/admin/audit",
	"magic":            "/admin/magic-links",
	"telemetry":        "/admin/telemetry",
	// V1's Custom Domains is the admin view of every domain's provisioning state (#1559) --
	// V2's Custom Domain Status page, not the personal table on the dashboard.
	"custom-domains": "/admin/vanity-domain-status",
	"network-health": "/admin/edge-health",
	"backups":        "/admin/backups",
	"maintenance":    "/admin/maintenance",
	"system":         "/admin/settings",
}
