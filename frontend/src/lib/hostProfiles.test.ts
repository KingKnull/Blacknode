import assert from "node:assert/strict";
import test from "node:test";
import type { Host } from "../../bindings/github.com/blacknode/blacknode/internal/store/models";
import { hostMatchesFilter, parseHostTags, recentHostProfiles } from "./hostProfiles.ts";

function host(overrides: Partial<Host>): Host {
  return {
    id: overrides.id ?? "id",
    name: overrides.name ?? "server",
    host: overrides.host ?? "10.0.0.1",
    port: 22,
    username: "ops",
    authMethod: "agent",
    tags: [],
    favorite: false,
    createdAt: 0,
    updatedAt: 0,
    lastConnectedAt: 0,
    ...overrides,
  } as Host;
}

test("host profile search includes alias and metadata", () => {
  const profile = host({ alias: "Primary web", environment: "production", tags: ["customer-facing"] });
  assert.equal(hostMatchesFilter(profile, "primary web"), true);
  assert.equal(hostMatchesFilter(profile, "customer-facing"), true);
  assert.equal(hostMatchesFilter(profile, "production"), true);
  assert.equal(hostMatchesFilter(profile, "missing"), false);
});

test("host profile tags are trimmed and deduplicated", () => {
  assert.deepEqual(parseHostTags(" prod, web,prod ,, customer-facing "), ["prod", "web", "customer-facing"]);
});

test("recent host profiles are newest first and bounded", () => {
  const older = host({ id: "older", lastConnectedAt: 10 });
  const newer = host({ id: "newer", lastConnectedAt: 30 });
  const middle = host({ id: "middle", lastConnectedAt: 20 });
  const source = [older, newer, middle];
  assert.deepEqual(recentHostProfiles(source, 2).map((h) => h.id), ["newer", "middle"]);
  assert.deepEqual(source.map((h) => h.id), ["older", "newer", "middle"]);
});
