// Per-environment colour tokens — kept in one place so HostList, Terminal,
// ExecPanel etc. all show the same dot/badge for the same env. Production is
// the only one we treat as a hazard cue.
//
// The colours are CSS custom properties rather than literals, and that is
// load-bearing: this module used to return hex, which meant the badges only
// ever suited the dark theme. On light they measured 1.5-2.8:1 — the dev badge
// was, in practice, invisible. Returning var() references hands the theme
// switch to CSS, where [data-theme="light"] already overrides every other
// colour in the app. The values and their measured contrast ratios live next to
// each other in app.css; envColor.test.ts reads that file and checks them.
//
// Every consumer interpolates these into a `style:` directive, so a var()
// reference is valid in the same places a hex was. Anything that needs a real
// component value (a canvas, an xterm theme object) must not use these.
export type EnvKind = "" | "dev" | "staging" | "production" | string;

export type EnvBadge = {
  color: string;
  bg: string;
  border: string;
  label: string;
  isProd: boolean;
};

/** Build the token triple for one env slug. */
function tokens(slug: string, label: string, isProd: boolean): EnvBadge {
  return {
    color: `var(--color-env-${slug})`,
    bg: `var(--color-env-${slug}-bg)`,
    border: `var(--color-env-${slug}-border)`,
    label,
    isProd,
  };
}

export function envBadge(env: EnvKind | undefined | null): EnvBadge {
  switch ((env ?? "").trim().toLowerCase()) {
    case "production":
      return tokens("prod", "PROD", true);
    case "staging":
      return tokens("stage", "STAGE", false);
    case "dev":
      return tokens("dev", "DEV", false);
    default:
      // Deliberately label-less: an unrecognised value must not be dressed up
      // as one of the three known environments. The colour is still returned
      // because the stripe renders regardless.
      return tokens("none", "", false);
  }
}
