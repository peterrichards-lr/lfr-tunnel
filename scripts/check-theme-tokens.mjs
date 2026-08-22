#!/usr/bin/env node
/**
 * Verifies that every CSS custom property the stylesheets reference is actually defined,
 * in every theme.
 *
 * This exists because the same defect kept recurring in different disguises:
 *
 *   #1217  --success and --danger were defined only in the base :root, so the light theme
 *          rendered colours tuned for a dark background as text and failed WCAG contrast.
 *   #1221  Five properties were referenced but defined in no theme at all. Two had no
 *          fallback, including the @media print block, which meant printing on the dark
 *          theme produced a near-black page.
 *
 * Both are invisible in review and invisible at build time: a var() that resolves to
 * nothing makes the declaration invalid and the browser silently drops it. The only way to
 * notice is to look at every reference against every theme, which is what this does.
 *
 * A referenced property is acceptable if it is defined in the base :root (inherited by all
 * themes) or defined in each theme block. A fallback -- var(--x, something) -- is accepted
 * as intentional, but reported, since it usually means a name is wrong rather than that a
 * default was wanted.
 */
import { readFileSync, readdirSync } from 'node:fs';
import { join, basename } from 'node:path';

const UI_SRC = join(process.cwd(), 'ui', 'src');
const THEMES = join(UI_SRC, 'themes');

// Comments are stripped before scanning: prose describing a token -- including the
// comments explaining these very bugs -- would otherwise register as a reference.
const stripComments = (css) => css.replace(/\/\*[\s\S]*?\*\//g, '');
const read = (p) => stripComments(readFileSync(p, 'utf8'));

// Every var(--x) reference outside the theme files themselves.
function collectReferences() {
  const refs = new Map(); // name -> { hasFallback }
  const walk = (dir) => {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      const full = join(dir, entry.name);
      if (entry.isDirectory()) {
        if (entry.name !== 'themes') walk(full);
        continue;
      }
      if (!entry.name.endsWith('.css')) continue;
      const css = read(full);
      for (const m of css.matchAll(/var\(\s*(--[a-z0-9-]+)\s*(,)?/g)) {
        const existing = refs.get(m[1]) || { hasFallback: false, files: new Set() };
        if (m[2]) existing.hasFallback = true;
        existing.files.add(basename(full));
        refs.set(m[1], existing);
      }
    }
  };
  walk(UI_SRC);
  return refs;
}

// Properties defined per theme file, split by whether they sit on a bare :root (inherited
// by every theme) or inside a theme-specific selector.
function collectDefinitions() {
  const base = new Set();
  const perTheme = new Map();
  for (const file of readdirSync(THEMES)) {
    if (!file.endsWith('.css') || file === 'index.css') continue;
    const css = read(join(THEMES, file));
    const defined = new Set();
    for (const m of css.matchAll(/^\s*(--[a-z0-9-]+)\s*:/gm)) defined.add(m[1]);
    perTheme.set(file, defined);

    // Anything declared under a bare `:root` is inherited by every theme. The selector is
    // often a list -- `:root, :root[data-theme="dark"] { ... }` -- so match the block and
    // then check whether any selector in it is an unqualified :root.
    for (const block of css.matchAll(/([^{}]+)\{([^}]*)\}/g)) {
      const selectors = block[1].split(',').map((sel) => sel.trim());
      if (!selectors.includes(':root')) continue;
      for (const m of block[2].matchAll(/(--[a-z0-9-]+)\s*:/g)) base.add(m[1]);
    }
  }
  return { base, perTheme };
}

// Colour values declared on the base :root, which every theme inherits unless it
// overrides them. A colour chosen for one theme's background is wrong on another's, so an
// un-overridden one is a bug waiting to be reported -- that is exactly what #1217 was:
// --success and --danger were dark-tuned, inherited by the light theme, and used as text.
//
// Checked separately from resolution because the failure is different: these *do* resolve,
// they just resolve to the wrong colour, which no amount of var() checking would notice.
function collectBaseColours() {
  const css = read(join(THEMES, 'dark.css'));
  const colours = new Map();
  for (const block of css.matchAll(/([^{}]+)\{([^}]*)\}/g)) {
    const selectors = block[1].split(',').map((sel) => sel.trim());
    if (!selectors.includes(':root')) continue;
    for (const m of block[2].matchAll(/(--[a-z0-9-]+)\s*:\s*([^;]+);/g)) {
      if (/^(#|rgba?\(|hsla?\()/.test(m[2].trim())) colours.set(m[1], m[2].trim());
    }
  }
  return colours;
}

const refs = collectReferences();
const { base, perTheme } = collectDefinitions();

const undefinedTokens = [];
const fallbackOnly = [];

for (const [name, info] of [...refs].sort()) {
  if (base.has(name)) continue;
  const missing = [...perTheme].filter(([, defs]) => !defs.has(name)).map(([f]) => f);
  if (missing.length === perTheme.size) {
    undefinedTokens.push({ name, info, missing });
  } else if (missing.length > 0) {
    undefinedTokens.push({ name, info, missing });
  } else if (info.hasFallback) {
    fallbackOnly.push(name);
  }
}

if (fallbackOnly.length > 0) {
  console.log('Referenced with a fallback (accepted, but check the name is right):');
  for (const n of fallbackOnly) console.log(`  ${n}`);
  console.log('');
}

if (undefinedTokens.length > 0) {
  console.error('❌ CSS custom properties that will not resolve:\n');
  for (const { name, info, missing } of undefinedTokens) {
    const where = missing.length === perTheme.size ? 'no theme defines it' : `missing from ${missing.join(', ')}`;
    const fb = info.hasFallback
      ? ' — it has a fallback, so it renders, but never follows the theme'
      : ' — the declaration is invalid and the browser drops it silently';
    console.error(`  ${name}: ${where}${fb}`);
    console.error(`      referenced in: ${[...info.files].join(', ')}`);
  }
  console.error('\nDefine it in every theme, or in the base :root if it is theme-independent.');
  process.exit(1);
}

// Second check: a colour inherited by every theme from the base :root.
const baseColours = collectBaseColours();
const themeFiles = [...perTheme.keys()].filter((f) => f !== 'dark.css');
const inherited = [...baseColours]
  .filter(([name]) => refs.has(name))
  .filter(([name]) => themeFiles.every((f) => !perTheme.get(f).has(name)));

if (inherited.length > 0) {
  console.error('❌ Colours inherited by every theme from the base :root:\n');
  for (const [name, value] of inherited) {
    console.error(`  ${name}: ${value} — no theme overrides it, so every theme renders this exact colour.`);
  }
  console.error(
    '\nA colour picked for one theme is usually wrong against another background. This is\n' +
      'how #1217 happened: --success and --danger were tuned for dark, inherited by light,\n' +
      'and used as text at 2.4:1 contrast. Give each theme its own value. If the colour is\n' +
      'genuinely theme-independent, set it explicitly in each theme so that is a decision\n' +
      'someone made rather than something nobody noticed.'
  );
  process.exit(1);
}

console.log(`✅ All ${refs.size} referenced CSS custom properties resolve in every theme.`);
console.log(`✅ No referenced colour is inherited un-overridden from the base :root.`);
