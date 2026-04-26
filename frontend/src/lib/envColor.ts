// Per-environment colour tokens — kept in one place so HostList, Terminal,
// ExecPanel etc. all show the same dot/badge for the same env. Production is
// the only one we treat as a hazard cue.
export type EnvKind = "" | "dev" | "staging" | "production" | string;

export function envBadge(env: EnvKind | undefined | null): {
  color: string;
  bg: string;
  border: string;
  label: string;
  isProd: boolean;
} {
  switch ((env ?? "").toLowerCase()) {
    case "production":
      return {
        color: "#ef4444",
        bg: "rgba(239, 68, 68, 0.12)",
        border: "rgba(239, 68, 68, 0.35)",
        label: "PROD",
        isProd: true,
      };
    case "staging":
      return {
        color: "#f59e0b",
        bg: "rgba(245, 158, 11, 0.12)",
        border: "rgba(245, 158, 11, 0.30)",
        label: "STAGE",
        isProd: false,
      };
    case "dev":
      return {
        color: "#10d9a0",
        bg: "rgba(16, 217, 160, 0.12)",
        border: "rgba(16, 217, 160, 0.25)",
        label: "DEV",
        isProd: false,
      };
    default:
      return {
        color: "#6b6b7c",
        bg: "rgba(107, 107, 124, 0.10)",
        border: "rgba(107, 107, 124, 0.20)",
        label: "",
        isProd: false,
      };
  }
}
