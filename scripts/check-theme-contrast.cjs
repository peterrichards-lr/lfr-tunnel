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
  m = /^rgba?\(\s*([\d.]+)[\s,]+([\d.]+)[\s,]+([\d.]+)/i.exec(v);
  if (m) return [Number(m[1]), Number(m[2]), Number(m[3])];
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

  const tokens = {};
  for (const m of css.matchAll(/(--[\w-]+)\s*:\s*([^;]+);/g)) {
    const parsed = parseColor(m[2]);
    if (parsed) tokens[m[1]] = parsed;
  }

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
      AA_TEXT,
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
      check(`white label on ${key}`, WHITE, fill, AA_TEXT);
      check(
        `white label on ${key} while hovered (brightness ${HOVER_BRIGHTNESS})`,
        WHITE,
        brighten(fill, HOVER_BRIGHTNESS),
        AA_TEXT,
      );
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
