import assert from "node:assert/strict";
import { describe, test } from "node:test";
import { isReadOnlyHost, policyBlock, summarizePolicyHosts } from "./commandPolicy.ts";

describe("command policy host labels", () => {
  test("recognizes read-only environment, group, and tag labels", () => {
    assert.equal(isReadOnlyHost({ environment: "read-only" }), true);
    assert.equal(isReadOnlyHost({ group: "Read Only" }), true);
    assert.equal(isReadOnlyHost({ tags: ["readonly-host"] }), true);
    assert.equal(isReadOnlyHost({ environment: "staging", tags: ["api"] }), false);
  });

  test("summarizes production and read-only hosts without duplicate names", () => {
    assert.deepEqual(
      summarizePolicyHosts([
        { name: "api", environment: "production" },
        { name: "api", environment: "production", tags: ["read-only"] },
        { name: "audit", group: "readonly" },
      ]),
      { production: ["api"], readOnly: ["api", "audit"] },
    );
  });
});

describe("command policy errors", () => {
  test("returns actionable read-only guidance", () => {
    assert.deepEqual(
      policyBlock("command policy denied: read-only host rejects mutating commands (classified as mutating)"),
      {
        reason: "read-only host rejects mutating commands (classified as mutating)",
        action: "Choose a read-only inspection command or remove the read-only host from the selection.",
      },
    );
  });

  test("returns approval guidance for production denials and ignores other errors", () => {
    assert.equal(
      policyBlock("command policy denied: production host requires explicit approval for destructive commands")?.action,
      "Review the command and run it again to open the production approval prompt.",
    );
    assert.equal(policyBlock("connection refused"), null);
  });
});
