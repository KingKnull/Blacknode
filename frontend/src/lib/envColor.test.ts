// Tests for the per-environment badge tokens.
//
// Two different things are checked here, and the second is the unusual one:
//
//  1. The mapping contract — production is the only environment flagged as a
//     hazard, matching is case-insensitive, and an unrecognised value degrades
//     to a neutral badge rather than throwing or falling through to prod.
//
//  2. The colours are actually legible, in both themes. These values are chosen
//     by eye, and "chosen by eye" is how the badges ended up at 1.5:1 on the
//     light theme — a bug that survived because nothing measured them. So this
//     file reads app.css, resolves the var() references envBadge returns, and
//     computes the WCAG ratio from the shipped values. It composites the
//     translucent badge fill over the surface first, because that blend is what
//     the label text actually sits on.
//
// Reading the stylesheet rather than duplicating the numbers is the point: a
// hard-coded copy would keep passing after someone edits app.css, which is
// exactly when the check needs to fire.

import { test, describe } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

import { envBadge } from "./envColor.ts";

type RGB = { r: number; g: number; b: number };

const CSS = readFileSync(new URL("../app.css", import.meta.url), "utf8");

/** Strip /* *\/ comments so brace-counting and var lookups can't trip on prose. */
function stripComments(css: string): string {
  return css.replace(/\/\*[\s\S]*?\*\//g, "");
}

/** Extract the declarations inside the block introduced by `header`. */
function block(header: string): Record<string, string> {
  const css = stripComments(CSS);
  const start = css.indexOf(header);
  assert.notEqual(start, -1, `app.css has no ${header} block`);
  let i = css.indexOf("{", start);
  assert.notEqual(i, -1, `${header} block has no opening brace`);
  let depth = 0;
  const from = i + 1;
  for (; i < css.length; i++) {
    if (css[i] === "{") depth++;
    else if (css[i] === "}" && --depth === 0) break;
  }
  assert.ok(depth === 0, `${header} block is unterminated`);
  const vars: Record<string, string> = {};
  for (const decl of css.slice(from, i).split(";")) {
    const m = /^\s*(--[\w-]+)\s*:\s*(.+?)\s*$/.exec(decl);
    if (m) vars[m[1]] = m[2];
  }
  return vars;
}

const DARK = block("@theme");
const LIGHT_OVERRIDES = block('[data-theme="light"]');
// The light theme is an override layer, exactly as the cascade applies it, so a
// token the light block forgets to redefine correctly inherits the dark value —
// and is then measured against light surfaces, which is what makes the missing
// override show up as a contrast failure rather than as a silent pass.
const LIGHT = { ...DARK, ...LIGHT_OVERRIDES };

const THEMES = { dark: DARK, light: LIGHT };
/** surface-0 is the page, surface-2 panels and dialogs, surface-3 input chrome. */
const SURFACE_KEYS = ["--color-surface-0", "--color-surface-2", "--color-surface-3"] as const;

/** Resolve a `var(--x)` reference (or a literal) against a theme's tokens. */
function resolve(value: string, vars: Record<string, string>): string {
  const m = /^var\(\s*(--[\w-]+)\s*\)$/.exec(value.trim());
  if (!m) return value.trim();
  const got = vars[m[1]];
  assert.ok(got, `app.css does not define ${m[1]}`);
  return resolve(got, vars);
}

function parseHex(hex: string): RGB {
  const m = /^#([0-9a-f]{6})$/i.exec(hex.trim());
  assert.ok(m, `not a 6-digit hex colour: ${hex}`);
  const n = parseInt(m[1], 16);
  return { r: (n >> 16) & 0xff, g: (n >> 8) & 0xff, b: n & 0xff };
}

function parseRGBA(css: string): { rgb: RGB; a: number } {
  const m = /^rgba?\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)\s*(?:,\s*([\d.]+)\s*)?\)$/i.exec(css.trim());
  assert.ok(m, `not an rgb/rgba colour: ${css}`);
  return {
    rgb: { r: Number(m[1]), g: Number(m[2]), b: Number(m[3]) },
    a: m[4] === undefined ? 1 : Number(m[4]),
  };
}

/** Composite a translucent colour over an opaque one (source-over). */
function over(fg: RGB, alpha: number, bg: RGB): RGB {
  return {
    r: alpha * fg.r + (1 - alpha) * bg.r,
    g: alpha * fg.g + (1 - alpha) * bg.g,
    b: alpha * fg.b + (1 - alpha) * bg.b,
  };
}

/** WCAG 2.x relative luminance. */
function luminance({ r, g, b }: RGB): number {
  const lin = (c: number) => {
    const s = c / 255;
    return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
  };
  return 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b);
}

/** WCAG 2.x contrast ratio, always >= 1. */
function contrast(a: RGB, b: RGB): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

const KNOWN = ["production", "staging", "dev"] as const;
const ALL_ENVS = [...KNOWN, ""] as const;

describe("envBadge mapping", () => {
  test("production is the only environment flagged as a hazard", () => {
    assert.equal(envBadge("production").isProd, true);
    for (const env of ["staging", "dev", "qa", "", "prod-like"]) {
      assert.equal(envBadge(env).isProd, false, `${env} should not be treated as production`);
    }
  });

  test("matching tolerates the casing and padding real data arrives with", () => {
    // Host.environment is free-text user input, so all of these occur.
    assert.equal(envBadge("PRODUCTION").label, "PROD");
    assert.equal(envBadge("Production").isProd, true);
    assert.equal(envBadge("  production  ").isProd, true);
    assert.equal(envBadge("Staging").label, "STAGE");
    assert.equal(envBadge("DEV").label, "DEV");
  });

  test("an unrecognised environment degrades to the neutral badge", () => {
    // "prod" and "pre-production" are the near-misses that matter: silently
    // promoting either to the hazard badge would be as wrong as demoting it.
    for (const env of ["qa", "sandbox", "prod", "pre-production", "production-eu"]) {
      const b = envBadge(env);
      assert.equal(b.label, "", `${env} should not borrow another env's label`);
      assert.equal(b.isProd, false);
      assert.equal(b.color, envBadge("").color);
    }
  });

  test("null, undefined and empty string are safe", () => {
    // environment is optional in the Wails bindings, so all three reach here.
    for (const env of [null, undefined, ""]) {
      const b = envBadge(env);
      assert.equal(b.label, "");
      assert.equal(b.isProd, false);
      assert.ok(b.color, "a colour is still required — the stripe always renders");
    }
  });

  test("labels stay short enough for a badge", () => {
    for (const env of KNOWN) {
      assert.ok(envBadge(env).label.length <= 5, `${env}: label too long for the badge`);
    }
  });

  test("each known environment gets a distinct colour and label", () => {
    const colors = KNOWN.map((e) => envBadge(e).color);
    const labels = KNOWN.map((e) => envBadge(e).label);
    assert.equal(new Set(colors).size, KNOWN.length, "two environments share a colour token");
    assert.equal(new Set(labels).size, KNOWN.length, "two environments share a label");
    // And none collides with the neutral fallback, or an unknown env would be
    // indistinguishable from a known one.
    assert.ok(!colors.includes(envBadge("").color));
  });
});

describe("envBadge tokens", () => {
  test("every returned value is a var() reference, not a baked colour", () => {
    // A literal here is the regression that produced the light-theme bug: it
    // cannot respond to [data-theme], so it is wrong on one theme by
    // construction.
    for (const env of ALL_ENVS) {
      const b = envBadge(env);
      for (const [field, value] of Object.entries({ color: b.color, bg: b.bg, border: b.border })) {
        assert.match(value, /^var\(--color-env-[\w-]+\)$/, `${env || "neutral"}.${field}`);
      }
    }
  });

  test("both themes define every token envBadge can return", () => {
    for (const [themeName, vars] of Object.entries(THEMES)) {
      for (const env of ALL_ENVS) {
        const b = envBadge(env);
        parseHex(resolve(b.color, vars));
        parseRGBA(resolve(b.bg, vars));
        parseRGBA(resolve(b.border, vars));
      }
      for (const key of SURFACE_KEYS) {
        parseHex(resolve(`var(${key})`, vars));
      }
    }
  });

  test("the light theme overrides every env token rather than inheriting", () => {
    // Inheriting a dark-theme colour is precisely the bug this file was written
    // for. Checking the override block directly — not the merged view — is what
    // catches a newly added env whose light variant was forgotten.
    for (const env of ALL_ENVS) {
      const b = envBadge(env);
      for (const value of [b.color, b.bg, b.border]) {
        const name = /^var\(\s*(--[\w-]+)\s*\)$/.exec(value)![1];
        assert.ok(
          name in LIGHT_OVERRIDES,
          `${name} has no [data-theme="light"] override — it will use the dark value on light`,
        );
        assert.notEqual(
          LIGHT_OVERRIDES[name],
          DARK[name],
          `${name} is overridden to the same value it already had`,
        );
      }
    }
  });

  test("backgrounds and borders are translucent", () => {
    // The badge sits on a selectable row; an opaque fill would hide the
    // selection and hover states underneath it.
    for (const [themeName, vars] of Object.entries(THEMES)) {
      for (const env of ALL_ENVS) {
        const b = envBadge(env);
        assert.ok(parseRGBA(resolve(b.bg, vars)).a < 1, `${themeName} ${env}: bg is opaque`);
        assert.ok(parseRGBA(resolve(b.border, vars)).a < 1, `${themeName} ${env}: border is opaque`);
      }
    }
  });
});

describe("envBadge contrast", () => {
  test("every badge clears WCAG AA (4.5:1) on its fill, in both themes", () => {
    for (const [themeName, vars] of Object.entries(THEMES)) {
      for (const env of ALL_ENVS) {
        const b = envBadge(env);
        const fg = parseHex(resolve(b.color, vars));
        const { rgb, a } = parseRGBA(resolve(b.bg, vars));
        for (const key of SURFACE_KEYS) {
          const surface = parseHex(resolve(`var(${key})`, vars));
          const ratio = contrast(fg, over(rgb, a, surface));
          assert.ok(
            ratio >= 4.5,
            `${themeName} ${env || "neutral"} on ${key}: ${ratio.toFixed(2)}:1 — below AA 4.5:1`,
          );
        }
      }
    }
  });

  test("every badge colour also clears AA on the bare surface", () => {
    // HostList draws the same colour as a 3px stripe with no fill behind it,
    // and the neutral token is included: it has no label, but the stripe is the
    // only cue an unlabelled host gets.
    for (const [themeName, vars] of Object.entries(THEMES)) {
      for (const env of ALL_ENVS) {
        const fg = parseHex(resolve(envBadge(env).color, vars));
        for (const key of SURFACE_KEYS) {
          const ratio = contrast(fg, parseHex(resolve(`var(${key})`, vars)));
          assert.ok(
            ratio >= 4.5,
            `${themeName} ${env || "neutral"} stripe on ${key}: ${ratio.toFixed(2)}:1`,
          );
        }
      }
    }
  });

  test("production stays the loudest badge in both themes", () => {
    // A product rule, not an accessibility one, and it needs its own assertion
    // because contrast does not deliver it: dev is the highest-contrast colour
    // of the four on dark. Loudness here is the fill and border alpha.
    for (const [themeName, vars] of Object.entries(THEMES)) {
      const prod = envBadge("production");
      const prodBg = parseRGBA(resolve(prod.bg, vars)).a;
      const prodBorder = parseRGBA(resolve(prod.border, vars)).a;
      for (const env of ["staging", "dev", ""]) {
        const b = envBadge(env);
        assert.ok(
          parseRGBA(resolve(b.bg, vars)).a <= prodBg,
          `${themeName} ${env || "neutral"}: fill is stronger than production's`,
        );
        assert.ok(
          parseRGBA(resolve(b.border, vars)).a <= prodBorder,
          `${themeName} ${env || "neutral"}: border is stronger than production's`,
        );
      }
    }
  });

  test("the contrast helper itself is calibrated", () => {
    // A silently broken luminance function would make every assertion above
    // pass vacuously, so pin it to ratios WCAG defines exactly.
    const white = { r: 255, g: 255, b: 255 };
    const black = { r: 0, g: 0, b: 0 };
    assert.ok(Math.abs(contrast(white, black) - 21) < 0.01, "white on black must be 21:1");
    assert.equal(contrast(white, white), 1, "a colour on itself must be 1:1");
    // #767676 on white is the canonical "exactly AA" grey.
    assert.ok(Math.abs(contrast(parseHex("#767676"), white) - 4.54) < 0.05);
    // And the composite must actually move the colour.
    assert.deepEqual(over(white, 0.5, black), { r: 127.5, g: 127.5, b: 127.5 });
  });

  test("the stylesheet parser found real tokens", () => {
    // If block() silently returned {} every contrast test would pass by
    // vacuous iteration, so assert the shape of what was parsed.
    assert.ok(Object.keys(DARK).length > 20, `parsed only ${Object.keys(DARK).length} dark tokens`);
    assert.ok(Object.keys(LIGHT_OVERRIDES).length > 10, "light override block looks empty");
    assert.equal(DARK["--color-surface-0"], "#0b0e14");
    assert.equal(LIGHT_OVERRIDES["--color-surface-0"], "#ffffff");
  });
});
