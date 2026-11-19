package server

// PortalV2Base is where the V2 single-page app is mounted: PORTAL_BASE in ui/src/portalBase.ts,
// which the router's basename and Vite's base both read. The paths in PortalV1SectionToV2 are
// relative to it. V2 moved here from /portalv2 in #2528.
const PortalV2Base = "/portal"

// v1SectionSystem is V1's System Settings section. Named only because "system" is also an
// audit target type elsewhere in this package, and goconst counts the two together.
const v1SectionSystem = "system"

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
	v1SectionSystem:  "/admin/settings",
}

// v1SectionRedirect returns the V2 URL path for an old V1 section link, `/portal/<section>` or
// `/admin/<section>` (#2528), or "" when the section is not one V1 had.
//
// The table maps by section alone. In V1 an admin's Tokens tab also held Token Permanence
// Requests and Reservations held Extension Requests; their old links land on the personal
// sections, which is where everyone else's go, and the admin queues are one click away in V2's
// Admin Zone.
func v1SectionRedirect(section string) string {
	target, ok := PortalV1SectionToV2[section]
	if !ok {
		return ""
	}
	return PortalV2Base + target
}
