// Platform badges for the host list.
//
// The backend stores an os-release ID or a uname sysname (see
// internal/service/platformdetect.go). Rather than ship distro logos, each
// platform gets a monogram on its own brand-ish colour: legible at the 14px the
// host row allows, no assets to load, and an unrecognised platform degrades to
// its own first letter rather than disappearing.

export type PlatformBadge = {
  /** One or two characters shown in the chip. */
  monogram: string;
  /** Full name for the tooltip. */
  label: string;
  /** CSS colour token or literal for the chip text and border. */
  color: string;
};

const NEUTRAL = "var(--color-text-4)";

// Colours are literals rather than theme tokens: these are brand identities,
// not part of the app's palette, and they read on both themes.
const PLATFORMS: Record<string, { monogram: string; label: string; color: string }> = {
  ubuntu: { monogram: "U", label: "Ubuntu", color: "#E95420" },
  debian: { monogram: "D", label: "Debian", color: "#D70A53" },
  alpine: { monogram: "A", label: "Alpine", color: "#0D597F" },
  fedora: { monogram: "F", label: "Fedora", color: "#51A2DA" },
  rhel: { monogram: "R", label: "Red Hat Enterprise Linux", color: "#EE0000" },
  centos: { monogram: "C", label: "CentOS", color: "#932279" },
  rocky: { monogram: "R", label: "Rocky Linux", color: "#10B981" },
  alma: { monogram: "A", label: "AlmaLinux", color: "#0F4266" },
  amazon: { monogram: "AZ", label: "Amazon Linux", color: "#FF9900" },
  oracle: { monogram: "O", label: "Oracle Linux", color: "#C74634" },
  suse: { monogram: "S", label: "SUSE", color: "#30BA78" },
  arch: { monogram: "A", label: "Arch Linux", color: "#1793D1" },
  mint: { monogram: "M", label: "Linux Mint", color: "#87CF3E" },
  gentoo: { monogram: "G", label: "Gentoo", color: "#54487A" },
  void: { monogram: "V", label: "Void Linux", color: "#478061" },
  nixos: { monogram: "N", label: "NixOS", color: "#5277C3" },
  linux: { monogram: "L", label: "Linux", color: "#F5C300" },
  darwin: { monogram: "", label: "macOS", color: "#A8AAAD" },
  windows: { monogram: "W", label: "Windows", color: "#00A4EF" },
  freebsd: { monogram: "B", label: "FreeBSD", color: "#AB2B28" },
  openbsd: { monogram: "O", label: "OpenBSD", color: "#F2CA30" },
  netbsd: { monogram: "N", label: "NetBSD", color: "#FF6600" },
  solaris: { monogram: "S", label: "Solaris", color: "#E87B35" },
};

/**
 * Resolve a stored platform id to its badge. Returns null when the host has no
 * platform yet, so callers can render nothing rather than a placeholder — the
 * value only arrives after the first successful connect.
 */
export function platformBadge(platform: string | null | undefined): PlatformBadge | null {
  const id = (platform ?? "").trim().toLowerCase();
  if (!id) return null;

  const known = PLATFORMS[id];
  if (known) return known;

  // An os-release ID we have no entry for ("opensuse-leap", "raspbian-ng") is
  // still worth badging: match on a known prefix before falling back, so
  // variants inherit their family's colour.
  for (const [key, badge] of Object.entries(PLATFORMS)) {
    if (id.startsWith(key)) return { ...badge, label: id };
  }

  return { monogram: id[0].toUpperCase(), label: id, color: NEUTRAL };
}
