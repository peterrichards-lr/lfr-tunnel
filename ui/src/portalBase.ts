/**
 * Where V2 is mounted (#2528): the router's basename and Vite's `base` both read this, so the
 * app, its assets and every link it builds move together.
 *
 * The server's copy is `PortalV2Base` in pkg/server/portal_sections.go, and
 * `portal_sections_test.go` holds the two equal. Keep this a single quoted string literal so that
 * check can parse it.
 */
export const PORTAL_BASE = '/portal';
