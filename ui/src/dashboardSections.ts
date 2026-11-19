/**
 * Dashboard sections addressable by path (#2527): `/portalv2/dashboard/<section>`.
 *
 * Tokens, reservations and tunnels live on the one Overview page in V2, where V1 gave each a
 * route of its own (`/portal/tokens`, ...). A path rather than a fragment keeps V2's links one
 * shape -- every admin page is already `/admin/<page>` -- and gives the V1 -> V2 redirects
 * (#2528) and the system emails somewhere specific to land.
 *
 * Key: the path segment. Value: the element id on the page, the same anchors the jump nav
 * (#1218) links to. An unknown segment falls back to the top of the dashboard.
 *
 * `pkg/server/portal_sections_test.go` reads this object to check that every V1 section the
 * server maps to `/dashboard/<section>` names a key here, and that every anchor here exists in
 * the dashboard markup. Keep it a flat object literal of quoted strings so that check can
 * parse it.
 */
export const DASHBOARD_SECTIONS: Readonly<Record<string, string>> = {
  tunnels: 'active-tunnels',
  reservations: 'reservations-overview',
  subdomains: 'registered-subdomains',
  'custom-domains': 'custom-domains',
  'custom-domain-status': 'custom-domain-status',
  tokens: 'access-tokens',
};

/** The element id for a section path segment, or null when the segment names no section. */
export function dashboardSectionAnchor(
  section: string | undefined,
): string | null {
  if (!section) return null;
  return Object.prototype.hasOwnProperty.call(DASHBOARD_SECTIONS, section)
    ? DASHBOARD_SECTIONS[section]
    : null;
}
