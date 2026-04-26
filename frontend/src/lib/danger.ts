// Heuristic detector for shell commands that can corrupt or destroy a host.
// Designed to err on the side of false positives — every match shows a
// confirmation dialog before the run, so a noisy match is annoying but not
// destructive. The reverse (missing a real fork bomb) is the failure mode we
// actually fear.
//
// We intentionally keep this client-side only — pattern lists evolve, and
// shipping them as code (not a server response) means there's no race where a
// stale list could let a destructive command slip through during an outage.

export type Danger = {
  level: "warn" | "block-without-confirm";
  reason: string;
  matched: string;
};

const DANGEROUS_PATTERNS: { re: RegExp; reason: string; level: Danger["level"] }[] = [
  {
    re: /\brm\s+(-[a-zA-Z]*r[a-zA-Z]*f|-[a-zA-Z]*f[a-zA-Z]*r|--recursive\s+--force)\s+(\/|~|\$HOME|\*)/,
    reason: "Recursive force-delete of root, home, or wildcard",
    level: "block-without-confirm",
  },
  {
    re: /\bmkfs(\.[a-z0-9]+)?\b/,
    reason: "Formatting a filesystem",
    level: "block-without-confirm",
  },
  {
    re: /\bdd\b[^|]*\bof=\/dev\/(sd|nvme|hd|vd|xvd)/,
    reason: "dd writing directly to a block device",
    level: "block-without-confirm",
  },
  {
    re: /:\(\)\s*\{\s*:\s*\|\s*:&\s*\}\s*;\s*:/,
    reason: "Fork-bomb signature",
    level: "block-without-confirm",
  },
  {
    re: />\s*\/dev\/(sd|nvme|hd|vd|xvd)/,
    reason: "Redirecting output to a block device",
    level: "block-without-confirm",
  },
  {
    re: /\bchmod\s+-R\s+777\s+\//,
    reason: "Recursive chmod 777 from root",
    level: "warn",
  },
  {
    re: /\bchown\s+-R\s+[^/\s]+\s+\//,
    reason: "Recursive chown from root",
    level: "warn",
  },
  {
    re: /\bshutdown\b|\breboot\b|\bhalt\b|\bpoweroff\b/,
    reason: "Host reboot / shutdown",
    level: "warn",
  },
  {
    re: /\biptables\s+-F\b|\bufw\s+--force\s+reset\b/,
    reason: "Wiping firewall rules",
    level: "warn",
  },
  {
    re: /\b(curl|wget)\b[^|]+\|\s*(sudo\s+)?(bash|sh|zsh)\b/,
    reason: "Piping curl|wget directly to a shell",
    level: "warn",
  },
  {
    re: /\b(dropdb|drop\s+database)\b/i,
    reason: "Dropping a database",
    level: "warn",
  },
  {
    re: /\bDROP\s+TABLE\b/i,
    reason: "DROP TABLE",
    level: "warn",
  },
];

export function checkCommand(cmd: string): Danger | null {
  const trimmed = cmd.trim();
  if (!trimmed) return null;
  for (const p of DANGEROUS_PATTERNS) {
    const m = trimmed.match(p.re);
    if (m) {
      return { level: p.level, reason: p.reason, matched: m[0] };
    }
  }
  return null;
}

// True if any host in the selection is tagged production.
export function anyProduction(envs: (string | undefined | null)[]): boolean {
  return envs.some((e) => (e ?? "").toLowerCase() === "production");
}
