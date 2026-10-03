// Tests for the host-list platform badge.
//
// The interesting behaviour is all in the fallbacks: the backend stores
// whatever os-release ID a host reported, which is an open set, so the mapping
// has to degrade sensibly rather than only handle the distros listed here.

import { test, describe } from "node:test";
import assert from "node:assert/strict";

import { platformBadge } from "./platform.ts";

describe("platformBadge", () => {
  test("a host with no platform yet gets no badge", () => {
    // Platform is only filled in after the first successful connect, so this
    // is the normal state for a freshly added host — it must render nothing
    // rather than a placeholder.
    for (const value of [null, undefined, "", "   "]) {
      assert.equal(platformBadge(value), null, `${JSON.stringify(value)} should have no badge`);
    }
  });

  test("known platforms get their own colour and label", () => {
    const ubuntu = platformBadge("ubuntu");
    assert.equal(ubuntu?.label, "Ubuntu");
    assert.equal(ubuntu?.monogram, "U");
    assert.match(ubuntu?.color ?? "", /^#[0-9A-Fa-f]{6}$/);

    // uname fallbacks are stored lowercased by the backend.
    assert.equal(platformBadge("darwin")?.label, "macOS");
    assert.equal(platformBadge("windows")?.label, "Windows");
    assert.equal(platformBadge("freebsd")?.label, "FreeBSD");
  });

  test("matching is case-insensitive and tolerates padding", () => {
    assert.equal(platformBadge("UBUNTU")?.label, "Ubuntu");
    assert.equal(platformBadge("  Debian  ")?.label, "Debian");
  });

  test("a distro variant inherits its family colour but keeps its own name", () => {
    // os-release reports these verbatim, and there is no entry for either.
    const leap = platformBadge("opensuse-leap");
    assert.equal(leap?.label, "opensuse-leap", "the variant should name itself");
    assert.equal(leap?.color, platformBadge("opensuse")?.color ?? leap?.color);

    const rocky9 = platformBadge("rocky9");
    assert.equal(rocky9?.color, platformBadge("rocky")?.color);
  });

  test("an entirely unknown platform still gets a readable badge", () => {
    const badge = platformBadge("plan9");
    assert.equal(badge?.monogram, "P");
    assert.equal(badge?.label, "plan9");
    assert.ok(badge?.color, "a colour is required — the chip always renders");
  });

  test("every badge has a non-empty label and colour", () => {
    // A blank monogram is allowed (macOS uses the Apple glyph), a blank label
    // or colour is not — both are user-visible.
    const ids = [
      "ubuntu", "debian", "alpine", "fedora", "rhel", "centos", "rocky", "alma",
      "amazon", "oracle", "suse", "arch", "mint", "gentoo", "void", "nixos",
      "linux", "darwin", "windows", "freebsd", "openbsd", "netbsd", "solaris",
    ];
    for (const id of ids) {
      const badge = platformBadge(id);
      assert.ok(badge, `${id} should resolve`);
      assert.ok(badge.label.length > 0, `${id} has no label`);
      assert.ok(badge.color.length > 0, `${id} has no colour`);
      assert.ok(badge.monogram.length <= 2, `${id} monogram is too long for the chip`);
    }
  });

  test("monograms stay short enough for the chip", () => {
    // The chip is sized for two characters; a longer one would overflow the row.
    assert.ok((platformBadge("amazon")?.monogram.length ?? 0) <= 2);
    assert.ok((platformBadge("some-very-long-distro-id")?.monogram.length ?? 0) <= 2);
  });
});
