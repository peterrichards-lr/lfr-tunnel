#!/usr/bin/env node
/**
 * Fails when a Portal V2 theme's button fills cannot carry the text placed on them.
 *
 * Written to be theme-agnostic: it discovers every file in the shared theme directory and checks
 * whatever it finds, so a theme added later is covered without anyone remembering this
 * script exists. That is the whole point -- #1458 shipped because the values were literals
 * in a component rule, where no amount of adding themes would have revealed the problem.
 *
 * What it encodes (see the .btn-danger comment in ui/src/index.css):
 *
 *   --danger              a FOREGROUND: btn-outline-danger's text and border.
 *                         >= 4.5:1 against the card. Darkening it to suit a fill breaks
 *                         this, which is why --danger-strong exists separately.
 *
 *   --danger-strong       a FILL under a white label. >= 4.5:1 against #fff, measured
 *   --danger-strong-alt   with filter: brightness(1.1) applied, because .btn-danger:hover
 *                         lightens it and that is the worst case -- not the resting state.
 *
 *   --danger              also the button's border, so >= 3:1 against the page for the
 *                         non-text contrast of a component boundary (WCAG 1.4.11).
 *
 *   --primary-strong      the same fill role for .btn-primary (#1514). Its second gradient
 *   --primary-strong-alt  stop used to be the literal #60a5fa, which put white text at
 *                         2.54:1 -- so the button failed AA in every theme, at rest, on
 *                         every page. Kept separate from --primary for the same reason
 *                         --danger-strong is separate from --danger: --primary is a
 *                         FOREGROUND (links, icons) and wants to be light on a dark card,
 *                         which is the opposite of what a fill under white text wants.
 *
 * Ratios are WCAG 2.x relative luminance in sRGB.
 */
'use strict';
const fs = require('fs');
const path = require('path');

// Shared by both portals since #1522: V2 @imports these files into its bundle, V1 links them.
// So this gate now covers Portal V1's colours too, which nothing checked while V1 kept its own
// copy of the tokens.
const THEMES = path.join(__dirname, '..', 'pkg', 'server', 'static', 'themes');
const AA_TEXT = 4.5;
const AA_NONTEXT = 3.0;
// AAA for normal text. Only applied to the prefers-contrast overrides, whose reason for
// existing is to clear a higher bar than the themes themselves are held to.
const AAA_TEXT = 7.0;
const HOVER_BRIGHTNESS = 1.1;
const WHITE = [255, 255, 255];

function parseColor(value) {
  const v = value.trim();
  let m = /^#([0-9a-f]{3}|[0-9a-f]{6})$/i.exec(v);
  if (m) {
    let h = m[1];
    if (h.length === 3)
      h = h
        .split('')
        .map((c) => c + c)
        .join('');
    return [0, 2, 4].map((i) => parseInt(h.slice(i, i + 2), 16));
  }
  // Alpha is KEPT (#1538). Most of the status fills are rgba over the card, and measuring them
  // as if opaque is measuring a colour nobody sees: rgba(139,92,246,0.15) on a near-black card
  // renders as a very dark violet, not as #8b5cf6.
  m = /^rgba?\(\s*([\d.]+)[\s,]+([\d.]+)[\s,]+([\d.]+)(?:[\s,/]+([\d.]+))?/i.exec(v);
  if (m) {
    const rgb = [Number(m[1]), Number(m[2]), Number(m[3])];
    if (m[4] !== undefined) rgb.alpha = Number(m[4]);
    return rgb;
  }
  return null;
}

const channel = (c) => {
  const s = c / 255;
  return s <= 0.04045 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
};
const luminance = ([r, g, b]) =>
  0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b);
function ratio(a, b) {
  const la = luminance(a);
  const lb = luminance(b);
  return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05);
}
const brighten = (rgb, f) => rgb.map((c) => Math.min(255, Math.round(c * f)));

// over composites a possibly-translucent colour onto an opaque one, which is what the browser
// paints. Without this a badge fill is measured at full strength and its label looks far more
// legible than it is (#1538).
const over = (fg, bg) => {
  const a = fg.alpha;
  if (a === undefined || a >= 1) return fg;
  return [0, 1, 2].map((i) => Math.round(fg[i] * a + bg[i] * (1 - a)));
};
const fmt = (n) => n.toFixed(2);

if (!fs.existsSync(THEMES)) {
  console.error(`check-theme-contrast: ${THEMES} not found`);
  process.exit(1);
}

const files = fs.readdirSync(THEMES).filter((f) => f.endsWith('.css'));
if (files.length === 0) {
  console.error(
    'check-theme-contrast: no theme files found -- the check would pass over nothing',
  );
  process.exit(1);
}

const failures = [];
let checks = 0;

for (const file of files) {
  const name = path.basename(file, '.css');
  const css = fs
    .readFileSync(path.join(THEMES, file), 'utf8')
    .replace(/\/\*[\s\S]*?\*\//g, '');

  // Split the file at the prefers-contrast block before collecting anything.
  //
  // A flat scan would let the high-contrast overrides shadow the base values, so the check would
  // silently measure the wrong palette -- and report OK either way, which is the shape of bug
  // this whole script exists to catch (#1521).
  const contrastAt = css.indexOf('@media (prefers-contrast: more)');
  const baseCss = contrastAt < 0 ? css : css.slice(0, contrastAt);
  const moreCss = contrastAt < 0 ? '' : css.slice(contrastAt);

  const collect = (text) => {
    const out = {};
    for (const m of text.matchAll(/(--[\w-]+)\s*:\s*([^;]+);/g)) {
      const parsed = parseColor(m[2]);
      if (parsed) out[m[1]] = parsed;
    }
    return out;
  };

  const tokens = collect(baseCss);
  const moreTokens = collect(moreCss);

  // A file that has not opted into ANY of the fill tokens is not a theme this check is about
  // -- ui/src/themes also holds a barrel index.css. Gated on all of them rather than on
  // --danger-strong alone, which would silently skip a theme that defined the primary fill and
  // not the danger one (#1514).
  const FILL_TOKENS = [
    '--danger-strong',
    '--danger-strong-alt',
    '--primary-strong',
    '--primary-strong-alt',
  ];
  if (!FILL_TOKENS.some((t) => tokens[t])) {
    console.log(`  ${name}: no button-fill tokens, skipped`);
    continue;
  }

  const page = tokens['--bg-card-solid'];
  if (!page) {
    failures.push(
      `${name}: --bg-card-solid is missing, so nothing can be measured against the page`,
    );
    continue;
  }

  // A theme whose whole reason for existing is contrast is held to AAA, not the AA the others
  // are (#1538). Otherwise it is just another palette -- it would pass while offering a reader
  // nothing the default theme did not already give them.
  const textBar = name === 'high-contrast' ? AAA_TEXT : AA_TEXT;

  const check = (label, fg, bg, min) => {
    checks++;
    const r = ratio(fg, bg);
    if (r < min) {
      failures.push(`${name}: ${label} is ${fmt(r)}:1, needs ${min}:1`);
    }
  };

  // Foreground role -- btn-outline-danger's text and border.
  if (tokens['--danger']) {
    check(
      '--danger as outline text on --bg-card-solid',
      tokens['--danger'],
      page,
      textBar,
    );
    // Boundary role -- .btn-danger's border against the page.
    check(
      '--danger as the solid button border against the page',
      tokens['--danger'],
      page,
      AA_NONTEXT,
    );
  }

  // Fill role -- white label, at rest and hovered.
  //
  // Both states are measured because which one is worst is not a constant: .btn-danger keeps
  // its gradient and lightens it, so hover is the worse case, while .btn-primary's failure was
  // at REST -- a flat hardcoded hover colour was overriding the gradient and happened to pass
  // (#1514). Checking only the state that bit last time is how the other one survives.
  const FILL_PAIRS = [
    ['--danger-strong', '--danger-strong-alt', '.btn-danger'],
    ['--primary-strong', '--primary-strong-alt', '.btn-primary'],
  ];
  for (const [first, second, button] of FILL_PAIRS) {
    if (!tokens[first] && !tokens[second]) continue;
    for (const key of [first, second]) {
      const fill = tokens[key];
      if (!fill) {
        failures.push(
          `${name}: ${key} is missing but its pair is defined -- ${button}'s gradient needs both`,
        );
        continue;
      }
      check(`white label on ${key}`, WHITE, fill, textBar);
      check(
        `white label on ${key} while hovered (brightness ${HOVER_BRIGHTNESS})`,
        WHITE,
        brighten(fill, HOVER_BRIGHTNESS),
        textBar,
      );
    }
  }

  // The status badges (#1538).
  //
  // Six triples -- danger, info, node, success, tunnels, warning -- each a label on a coloured
  // fill, and none of them checked until now. The gate covered the button fills and the two body
  // text colours, which is nine tokens out of sixty-four; these are the rest of the palette that
  // actually renders text, and therefore the rest that can fail a reader.
  //
  // The fills are rgba over the card, so they are composited first. Measuring them opaque was
  // the other half of why this went unnoticed: it flatters every one of them.
  const STATUS_FAMILIES = ['danger', 'info', 'node', 'success', 'tunnels', 'warning'];
  for (const family of STATUS_FAMILIES) {
    const fg = tokens[`--status-${family}-text`];
    const bgToken = tokens[`--status-${family}-bg`];
    if (!fg || !bgToken) continue;

    const card = tokens['--bg-card-solid'];
    if (!card) continue;
    const bg = over(bgToken, card);

    checks++;
    const r = ratio(fg, bg);
    if (r < textBar) {
      failures.push(
        `${name}: --status-${family}-text on --status-${family}-bg is ${fmt(r)}:1, needs ${textBar}:1 ` +
          `(the fill composited over the card, which is what a reader sees)`,
      );
    }

    // The --status-*-border tokens are deliberately NOT gated at 3:1.
    //
    // Measured, all eighteen sit between 1.25:1 and 2.02:1 against the card. That looks alarming
    // until you ask what 1.4.11 is protecting: it requires 3:1 for a UI component or a graphic
    // you must perceive to understand the content. A badge is identified by its FILL and its
    // LABEL -- both of which are checked above and pass -- and the border is a refinement of an
    // edge that is already visible. Nothing is lost if it is subtle.
    //
    // Gating it would force eighteen palette changes across three themes and make every badge
    // heavier, for no reader who could not already see the badge. Recorded here rather than
    // silently omitted, because "we chose not to" and "we forgot" look identical in a gate.
    //
    // Where a border IS the only boundary -- .btn-danger's, against the page -- it is checked,
    // further up.
  }

  // prefers-contrast: more must actually RAISE contrast, and reach AAA for normal text.
  //
  // A theme that has not opted in is not failed for it -- the media block is optional -- but one
  // that has opted in and made things no better is worse than not having done it, because it
  // reads as handled.
  if (Object.keys(moreTokens).length > 0) {
    const surface = moreTokens['--bg-card-solid'] || tokens['--bg-card-solid'];
    if (!surface) {
      failures.push(
        `${name}: prefers-contrast overrides exist but --bg-card-solid does not, so nothing can be measured against`,
      );
    } else {
      for (const key of ['--text-main', '--text-muted']) {
        const raised = moreTokens[key];
        if (!raised) continue;
        checks++;
        const r = ratio(raised, surface);
        if (r < AAA_TEXT) {
          failures.push(
            `${name}: prefers-contrast ${key} is ${fmt(r)}:1 on the card, needs ${AAA_TEXT}:1 -- ` +
              `raising contrast is the entire point of the block`,
          );
        }
        // And it must be an improvement, not a sideways move.
        const before = tokens[key];
        if (before) {
          checks++;
          const was = ratio(before, tokens['--bg-card-solid'] || surface);
          if (r < was) {
            failures.push(
              `${name}: prefers-contrast ${key} LOWERS contrast, ${fmt(was)}:1 -> ${fmt(r)}:1`,
            );
          }
        }
      }
    }
  }

  if (!tokens['--danger-glow']) {
    failures.push(
      `${name}: --danger-glow is missing -- .btn-danger's shadow would fall back to nothing`,
    );
  }
}

if (failures.length) {
  console.error('check-theme-contrast: button colours below WCAG AA\n');
  for (const f of failures) console.error(`  ${f}`);
  console.error(
    '\nSee the .btn-danger comment in ui/src/index.css for which token plays which role.',
  );
  console.error(
    'A fill needs to be darker for a white label; a foreground needs to be lighter on a dark card.',
  );
  process.exit(1);
}

console.log(
  `check-theme-contrast: OK -- ${checks} contrast checks across ${files.length} theme(s)`,
);
