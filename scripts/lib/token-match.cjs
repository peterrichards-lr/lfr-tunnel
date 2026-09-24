#!/usr/bin/env node
/**
 * token-match.cjs -- "is this marker present?", answered on token boundaries rather than by
 * containment.
 *
 * The class (#2201, #2208): a gate decides a name is still there with String.includes(), so a
 * rename that EXTENDS the old name goes on satisfying it. `session_keys_unacked` is still
 * "found" in a file that only has `session_keys_unacked_MUTANT`; a print rule targeting
 * `.summary` is still "live" against markup that only has `summary-row`. The gate then vouches
 * for something that no longer exists, which is the one thing these gates exist to prevent --
 * a rule matching nothing looks identical to a rule that works.
 *
 * It lives here, shared, because the first fix (#2201) fixed check-portal-parity.cjs and a sweep
 * for the SHAPE rather than for the symbol immediately found the same defect one file over. A
 * second copy would have been a third one waiting to happen; this is the same "one rule, one
 * home" reasoning #2211 applied to the Go side in the same PR.
 *
 * The scope this does NOT have, stated where it can be asserted rather than only described:
 * matching is lexical, over a blob of source. A marker that is a whole word of English prose --
 * "summary" in a sentence -- still counts as present, because nothing here knows the difference
 * between prose and markup. Widening it that far would need a parser, not a stricter boundary.
 */

'use strict';

// What can continue a symbol, an i18n key, a CSS class or an HTML id. `-` is in here because
// half of these names are kebab-case, and without it `reservation-ac-passcode-confirm` would go
// on matching `reservation-ac-passcode-confirm-v2` -- the very rename this is here to catch.
const TOKEN_CHAR = /[A-Za-z0-9_$-]/;

/**
 * Where `marker` occurs in `src` as a WHOLE marker, or -1.
 *
 * Boundaries are required only on the sides where the marker's own edge is a token character, the
 * same rule `\b` uses: `openReservationAcModal(` ends in a delimiter already, so nothing may
 * precede it but anything may follow. A marker containing `/` is a URL path and is matched as a
 * plain substring on purpose -- `/api/x` should still be found in a tree that has moved it to
 * `/api/x/bulk`, because the capability is reached either way. A CSS class name never contains a
 * `/`, so that exception simply never applies to a print selector.
 *
 * The strictness is derived from the marker's SHAPE, not from a per-entry `exact:` flag. A flag
 * is something to forget: a marker added later would silently inherit the loose behaviour, which
 * is how the defect survives its own fix.
 *
 * Done with indexOf rather than a RegExp so the marker needs no escaping: these contain `/`, `(`,
 * `)` and `-`, and an escape helper is one more thing that can be wrong about a marker.
 */
function findMarker(src, marker) {
  if (!marker) return -1;
  if (marker.includes('/')) return src.indexOf(marker);
  const boundLeft = TOKEN_CHAR.test(marker[0]);
  const boundRight = TOKEN_CHAR.test(marker[marker.length - 1]);
  for (
    let at = src.indexOf(marker);
    at !== -1;
    at = src.indexOf(marker, at + 1)
  ) {
    const before = at === 0 ? '' : src[at - 1];
    const after = src[at + marker.length] || '';
    if (boundLeft && before && TOKEN_CHAR.test(before)) continue;
    if (boundRight && after && TOKEN_CHAR.test(after)) continue;
    return at;
  }
  return -1;
}

module.exports = { TOKEN_CHAR, findMarker };
