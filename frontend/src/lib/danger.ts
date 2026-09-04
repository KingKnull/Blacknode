// Heuristic detector for shell commands that can corrupt or destroy a host, or
// cut the connection you are typing them over.
//
// Designed to err on the side of false positives — every match shows a
// confirmation dialog before the run, so a noisy match is annoying but not
// destructive. The reverse (missing a real fork bomb) is the failure mode we
// actually fear. `echo "rm -rf /"` will match, and that is the correct trade.
//
// We intentionally keep this client-side only — pattern lists evolve, and
// shipping them as code (not a server response) means there's no race where a
// stale list could let a destructive command slip through during an outage.
//
// Limits worth being honest about: this is textual, so it does not defeat
// deliberate obfuscation (`r''m -rf /`, base64 | sh, a destructive script
// invoked by name). It is a guard against mistakes and muscle memory, not
// against an adversary at the keyboard — the person running the command already
// has the shell.

export type DangerCategory =
  | "data-loss" // destroys data or filesystems
  | "lockout" // may sever your own access to the host
  | "availability" // stops services or reboots the machine
  | "irreversible" // cloud/IaC teardown that cannot be undone locally
  | "history"; // hides what was done

export type Danger = {
  level: "warn" | "block-without-confirm";
  reason: string;
  matched: string;
  category: DangerCategory;
};

type Pattern = {
  re: RegExp;
  reason: string;
  level: Danger["level"];
  category: DangerCategory;
};

const DANGEROUS_PATTERNS: Pattern[] = [
  // ---------------------------------------------------------------------------
  // Filesystem and block-device destruction. Nothing here is recoverable.
  // ---------------------------------------------------------------------------
  {
    // Covers ${HOME} and a trailing /* in addition to the bare forms; `rm -rf
    // /*` is the version people actually type by accident when a variable is
    // empty.
    re: /\brm\s+(-[a-zA-Z]*r[a-zA-Z]*f|-[a-zA-Z]*f[a-zA-Z]*r|--recursive\s+--force)\s+(--no-preserve-root\s+)?(\/\s*$|\/\*|~|\$HOME\b|\$\{HOME\}|\*\s*$)/,
    reason: "Recursive force-delete of root, home, or a bare wildcard",
    level: "block-without-confirm",
    category: "data-loss",
  },
  {
    re: /\brm\s+-[a-zA-Z]*r[a-zA-Z]*f?\s+(\/(etc|var|usr|boot|bin|sbin|lib|opt|srv|home|root)\b)/,
    reason: "Recursive delete of a system directory",
    level: "block-without-confirm",
    category: "data-loss",
  },
  {
    re: /\bmkfs(\.[a-z0-9]+)?\b/,
    reason: "Formatting a filesystem",
    level: "block-without-confirm",
    category: "data-loss",
  },
  {
    re: /\bdd\b[^|]*\bof=\/dev\/(sd|nvme|hd|vd|xvd|mmcblk|loop)/,
    reason: "dd writing directly to a block device",
    level: "block-without-confirm",
    category: "data-loss",
  },
  {
    re: />\s*\/dev\/(sd|nvme|hd|vd|xvd|mmcblk)/,
    reason: "Redirecting output to a block device",
    level: "block-without-confirm",
    category: "data-loss",
  },
  {
    re: /\b(wipefs|blkdiscard|sgdisk\s+(--zap-all|-Z)|shred\b[^|]*\/dev\/)/,
    reason: "Erasing a disk or its partition table",
    level: "block-without-confirm",
    category: "data-loss",
  },
  {
    re: /\b(lvremove|vgremove|pvremove|zpool\s+destroy|zfs\s+destroy|mdadm\s+--zero-superblock)\b/,
    reason: "Destroying a volume group, array, or ZFS dataset",
    level: "block-without-confirm",
    category: "data-loss",
  },
  {
    re: /\bcryptsetup\s+(luksFormat|luksErase|erase)\b/,
    reason: "Reformatting or erasing a LUKS volume",
    level: "block-without-confirm",
    category: "data-loss",
  },
  {
    re: /\bfind\s+\/\S*[^|]*\s-(delete|exec\s+rm)\b/,
    reason: "find rooted at / with -delete or -exec rm",
    level: "block-without-confirm",
    category: "data-loss",
  },
  {
    re: /:\(\)\s*\{\s*:\s*\|\s*:&\s*\}\s*;\s*:/,
    reason: "Fork-bomb signature",
    level: "block-without-confirm",
    category: "availability",
  },

  // ---------------------------------------------------------------------------
  // Lockout. Specific to a remote-access tool: these succeed, and the success
  // is what disconnects you. Ranked as block-without-confirm because the
  // recovery path is usually console access you may not have.
  // ---------------------------------------------------------------------------
  {
    // Both orders, because the two tools disagree: `systemctl stop sshd` but
    // `service sshd stop`. Matching only the systemctl form meant every SysV
    // host silently skipped the most important check in this file.
    re: /\bsystemctl\s+(stop|disable|mask)\s+(ssh|sshd|openssh-server)\b|\bservice\s+(ssh|sshd|openssh-server)\s+(stop|disable)\b/,
    reason: "Stopping SSH — you will lose this connection and cannot reconnect",
    level: "block-without-confirm",
    category: "lockout",
  },
  {
    re: /\biptables\s+-P\s+INPUT\s+DROP\b|\bnft\s+flush\s+ruleset\b/,
    reason: "Default-deny or flushed firewall — likely to cut your session",
    level: "block-without-confirm",
    category: "lockout",
  },
  {
    re: /\bip\s+link\s+set\s+\S+\s+down\b|\bifdown\s+\S+/,
    reason: "Bringing a network interface down",
    level: "block-without-confirm",
    category: "lockout",
  },
  {
    re: /(^|[\s;&|])>\s*~?\/?[^\s;&|]*\.ssh\/authorized_keys\b/,
    reason: "Truncating authorized_keys — removes key access to this host",
    level: "block-without-confirm",
    category: "lockout",
  },
  {
    re: /\biptables\s+-F\b|\bufw\s+--force\s+reset\b|\bufw\s+default\s+deny\s+incoming\b/,
    reason: "Wiping or default-denying firewall rules",
    level: "warn",
    category: "lockout",
  },
  {
    re: /\b(userdel|passwd\s+-l|usermod\s+(-L|--lock))\b/,
    reason: "Deleting or locking a user account",
    level: "warn",
    category: "lockout",
  },

  // ---------------------------------------------------------------------------
  // Availability: reversible, but the host goes away while you watch.
  // ---------------------------------------------------------------------------
  {
    re: /\bshutdown\b|\breboot\b|\bhalt\b|\bpoweroff\b|\binit\s+0\b/,
    reason: "Host reboot / shutdown",
    level: "warn",
    category: "availability",
  },
  {
    re: /\bsystemctl\s+(stop|disable|mask)\s+\S+|\bservice\s+\S+\s+(stop|disable)\b/,
    reason: "Stopping or disabling a service",
    level: "warn",
    category: "availability",
  },
  {
    re: /\b(pkill|killall)\s+(-9|-KILL|-SIGKILL)\b/,
    reason: "SIGKILL by process name",
    level: "warn",
    category: "availability",
  },
  {
    re: /\b(apt-get|apt|yum|dnf)\s+(remove|purge|autoremove)\b|\bpacman\s+-R/,
    reason: "Removing installed packages",
    level: "warn",
    category: "availability",
  },
  {
    re: /\bcrontab\s+-r\b/,
    reason: "crontab -r deletes every scheduled job with no confirmation",
    level: "warn",
    category: "data-loss",
  },

  // ---------------------------------------------------------------------------
  // Permissions: not destructive on their own, but they break things broadly
  // and are tedious to undo.
  // ---------------------------------------------------------------------------
  {
    re: /\bchmod\s+-R\s+(777|000)\s+\//,
    reason: "Recursive chmod from root",
    level: "warn",
    category: "data-loss",
  },
  {
    re: /\bchown\s+-R\s+[^/\s]+\s+\//,
    reason: "Recursive chown from root",
    level: "warn",
    category: "data-loss",
  },

  // ---------------------------------------------------------------------------
  // Remote code execution from the network.
  // ---------------------------------------------------------------------------
  {
    re: /\b(curl|wget)\b[^|]+\|\s*(sudo\s+)?(bash|sh|zsh|python3?|perl)\b/,
    reason: "Piping a downloaded script straight into an interpreter",
    level: "warn",
    category: "irreversible",
  },

  // ---------------------------------------------------------------------------
  // Databases. Matched case-insensitively since SQL is usually shouted.
  // ---------------------------------------------------------------------------
  {
    re: /\b(dropdb|drop\s+database)\b/i,
    reason: "Dropping a database",
    level: "block-without-confirm",
    category: "data-loss",
  },
  {
    re: /\bDROP\s+(TABLE|SCHEMA)\b/i,
    reason: "DROP TABLE / DROP SCHEMA",
    level: "warn",
    category: "data-loss",
  },
  {
    re: /\bTRUNCATE\s+(TABLE\s+)?\w/i,
    reason: "TRUNCATE empties a table and is not transactional on all engines",
    level: "warn",
    category: "data-loss",
  },
  {
    // A DELETE or UPDATE with no WHERE hits every row. The lookahead has to
    // stop at a statement boundary, or a later statement's WHERE would excuse
    // an unqualified one earlier in the same line.
    re: /\b(DELETE\s+FROM|UPDATE)\s+[^;]*?(?=;|$)/i,
    reason: "DELETE / UPDATE with no WHERE clause affects every row",
    level: "warn",
    category: "data-loss",
  },
  {
    re: /\b(FLUSHALL|FLUSHDB)\b/i,
    reason: "Flushing the entire Redis keyspace",
    level: "warn",
    category: "data-loss",
  },

  // ---------------------------------------------------------------------------
  // Containers, orchestration, IaC. Increasingly the actual blast radius.
  // ---------------------------------------------------------------------------
  {
    re: /\bterraform\s+destroy\b/,
    reason: "terraform destroy tears down managed infrastructure",
    level: "block-without-confirm",
    category: "irreversible",
  },
  {
    re: /\bterraform\s+apply\b[^|]*\s-auto-approve\b/,
    reason: "terraform apply with -auto-approve skips the plan review",
    level: "warn",
    category: "irreversible",
  },
  {
    re: /\bkubectl\s+delete\b[^|]*(--all\b|\bnamespace\b|\bns\b|-f\b)/,
    reason: "kubectl delete against a namespace, manifest, or --all",
    level: "block-without-confirm",
    category: "irreversible",
  },
  {
    re: /\bkubectl\s+(delete|drain)\b/,
    reason: "Deleting or draining Kubernetes resources",
    level: "warn",
    category: "availability",
  },
  {
    re: /\bhelm\s+(uninstall|delete)\b/,
    reason: "Uninstalling a Helm release",
    level: "warn",
    category: "availability",
  },
  {
    re: /\bdocker\s+(system\s+prune|volume\s+prune|volume\s+rm)\b/,
    reason: "Pruning Docker volumes or system data deletes container state",
    level: "warn",
    category: "data-loss",
  },
  {
    re: /\baws\s+s3\s+(rb|rm)\b[^|]*(--recursive|--force)\b/,
    reason: "Recursive S3 delete or bucket removal",
    level: "block-without-confirm",
    category: "irreversible",
  },

  // ---------------------------------------------------------------------------
  // Version control. Not host-destructive, but it destroys other people's work,
  // which is harder to get back than a file.
  // ---------------------------------------------------------------------------
  {
    re: /\bgit\s+push\b[^|]*\s(--force(?!-with-lease)|-f)\b/,
    reason: "git push --force overwrites remote history (use --force-with-lease)",
    level: "warn",
    category: "irreversible",
  },
  {
    re: /\bgit\s+(reset\s+--hard|clean\s+-[a-zA-Z]*f[a-zA-Z]*d|clean\s+-[a-zA-Z]*d[a-zA-Z]*f)\b/,
    reason: "Discarding uncommitted work",
    level: "warn",
    category: "data-loss",
  },

  // ---------------------------------------------------------------------------
  // Covering tracks. Flagged not because it breaks the host but because it
  // breaks the record of what happened to it.
  // ---------------------------------------------------------------------------
  {
    re: /\bhistory\s+-c\b|>\s*~?\/?\S*\.bash_history\b|\bunset\s+HISTFILE\b/,
    reason: "Clearing shell history",
    level: "warn",
    category: "history",
  },
  {
    // Three shapes, because log-wiping has three idioms: delete the tree,
    // redirect nothing over a file (`: > file`, `cat /dev/null > file`), or
    // truncate(1) it. Note there is no \b before the `:` alternative — `:` is
    // not a word character, so \b can never match before it at the start of a
    // string, which is exactly where this idiom appears.
    re: /\brm\s+(-\S+\s+)?\/var\/log\b|(:|\/dev\/null)\s*>\s*\/var\/log\/|\btruncate\b[^|;&]*\/var\/log\//,
    reason: "Deleting or truncating system logs",
    level: "warn",
    category: "history",
  },
];

const RANK: Record<Danger["level"], number> = {
  warn: 1,
  "block-without-confirm": 2,
};

/**
 * checkCommand returns the single most severe match, or null.
 *
 * It deliberately does not return the *first* match. The list is grouped by
 * theme for readability, so a `warn` pattern can easily sit above a
 * `block-without-confirm` one that also matches — and with first-match-wins,
 * adding a pattern in the wrong place would silently downgrade an existing
 * block. Ranking makes list order a presentation choice rather than a
 * correctness constraint.
 */
export function checkCommand(cmd: string): Danger | null {
  const all = checkCommandAll(cmd);
  return all.length > 0 ? all[0] : null;
}

/**
 * checkCommandAll returns every match, most severe first, so a confirmation
 * dialog can show all the reasons a command is being questioned rather than
 * just the worst one. `terraform destroy | tee /dev/sda` deserves both lines.
 */
export function checkCommandAll(cmd: string): Danger[] {
  const trimmed = cmd.trim();
  if (!trimmed) return [];
  const hits: Danger[] = [];
  for (const p of DANGEROUS_PATTERNS) {
    const m = trimmed.match(p.re);
    if (!m) continue;
    // A DELETE/UPDATE match only counts when the statement has no WHERE; the
    // regex can't express "absence of a token later in its own match" without
    // becoming unreadable, so the check lives here.
    if (p.category === "data-loss" && /^(DELETE\s+FROM|UPDATE)\b/i.test(m[0]) && /\bWHERE\b/i.test(m[0])) {
      continue;
    }
    hits.push({ level: p.level, reason: p.reason, matched: m[0].trim(), category: p.category });
  }
  return hits.sort((a, b) => RANK[b.level] - RANK[a.level]);
}

// True if any host in the selection is tagged production.
export function anyProduction(envs: (string | undefined | null)[]): boolean {
  return envs.some((e) => (e ?? "").toLowerCase() === "production");
}
