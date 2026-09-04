// Tests for cURL parsing, rendering, and {{var}} substitution.
//
// The round-trip property is the one that matters most: a user pastes cURL,
// saves the request, and later copies it back out. If parseCurl and toCurl
// disagree about quoting, the command they copy is subtly different from the one
// they pasted — and they will only find out when it hits production.

import { test, describe } from "node:test";
import assert from "node:assert/strict";

import { parseCurl, toCurl, substituteVars, type ParsedRequest } from "./httpCurl.ts";

describe("parseCurl", () => {
  test("parses a plain GET", () => {
    const r = parseCurl("curl https://api.example.com/v1/health");
    assert.equal(r.method, "GET");
    assert.equal(r.url, "https://api.example.com/v1/health");
    assert.deepEqual(r.headers, {});
    assert.equal(r.body, "");
    assert.equal(r.insecure, false);
  });

  test("parses method, headers, and body", () => {
    const r = parseCurl(
      `curl -X POST https://api.example.com/users -H 'Content-Type: application/json' -H "Authorization: Bearer abc123" -d '{"name":"ada"}'`,
    );
    assert.equal(r.method, "POST");
    assert.equal(r.url, "https://api.example.com/users");
    assert.deepEqual(r.headers, {
      "Content-Type": "application/json",
      Authorization: "Bearer abc123",
    });
    assert.equal(r.body, '{"name":"ada"}');
  });

  test("infers POST from a body when no method is given", () => {
    const r = parseCurl(`curl https://api.example.com/users -d 'name=ada'`);
    assert.equal(r.method, "POST");
  });

  test("an explicit -X wins over the body-implied POST in either order", () => {
    assert.equal(parseCurl(`curl -X PUT https://x.test -d 'a=1'`).method, "PUT");
    assert.equal(parseCurl(`curl -d 'a=1' -X PUT https://x.test`).method, "PUT");
  });

  test("handles line continuations from a copy-pasted multi-line command", () => {
    const r = parseCurl(`curl -X POST \\
  https://api.example.com/users \\
  -H 'Accept: application/json' \\
  -d '{"a":1}'`);
    assert.equal(r.method, "POST");
    assert.equal(r.url, "https://api.example.com/users");
    assert.deepEqual(r.headers, { Accept: "application/json" });
    assert.equal(r.body, '{"a":1}');
  });

  test("recognises --insecure and -k", () => {
    assert.equal(parseCurl("curl -k https://x.test").insecure, true);
    assert.equal(parseCurl("curl --insecure https://x.test").insecure, true);
    assert.equal(parseCurl("curl https://x.test").insecure, false);
  });

  test("honours --url and -I/-G", () => {
    assert.equal(parseCurl("curl --url https://x.test/a").url, "https://x.test/a");
    assert.equal(parseCurl("curl -I https://x.test").method, "HEAD");
    assert.equal(parseCurl("curl -G https://x.test").method, "GET");
  });

  test("does not lose the URL to an unmodelled flag's argument", () => {
    // This is the case the flag-skipping heuristic exists for: --resolve takes
    // a value, and a naive parser assigns that value as the URL.
    const r = parseCurl(
      "curl --resolve example.com:443:1.2.3.4 --compressed -L https://example.com/api",
    );
    assert.equal(r.url, "https://example.com/api");
  });

  test("keeps a header value containing colons intact", () => {
    const r = parseCurl(`curl https://x.test -H 'X-Time: 12:30:00'`);
    assert.equal(r.headers["X-Time"], "12:30:00");
  });

  test("tolerates a header written without a space after the colon", () => {
    const r = parseCurl(`curl https://x.test -H 'Content-Type:application/json'`);
    assert.equal(r.headers["Content-Type"], "application/json");
  });

  test("rejects input that is not a curl command", () => {
    assert.throws(() => parseCurl("wget https://x.test"), /must start with/);
    assert.throws(() => parseCurl(""), /must start with/);
  });

  test("rejects a curl command with no URL", () => {
    assert.throws(() => parseCurl("curl -X POST -H 'Accept: */*'"), /no URL/);
  });
});

describe("toCurl", () => {
  test("renders method, url, headers, body and insecure", () => {
    const out = toCurl({
      method: "POST",
      url: "https://api.example.com/users",
      headers: { Accept: "application/json" },
      body: '{"a":1}',
      insecure: true,
    });
    assert.match(out, /^curl -X POST 'https:\/\/api\.example\.com\/users'/);
    assert.match(out, /-H 'Accept: application\/json'/);
    assert.match(out, /--data '\{"a":1\}'/);
    assert.match(out, /--insecure/);
  });

  test("skips empty header names", () => {
    const out = toCurl({
      method: "GET",
      url: "https://x.test",
      headers: { "": "orphan" },
      body: "",
      insecure: false,
    });
    assert.ok(!out.includes("orphan"), `empty header name leaked: ${out}`);
  });

  test("single-quotes values so the shell cannot interpret them", () => {
    const out = toCurl({
      method: "GET",
      url: "https://x.test/?q=$(whoami)&b=`id`",
      headers: {},
      body: "",
      insecure: false,
    });
    // Inside single quotes neither $() nor backticks are expanded, which is the
    // whole reason shellQuote does not use double quotes.
    assert.ok(out.includes("'https://x.test/?q=$(whoami)&b=`id`'"), out);
  });
});

describe("parseCurl / toCurl round-trip", () => {
  // Annotated rather than inferred: without it TypeScript widens each entry to
  // its own literal shape, and `headers: {}` stops being a Record<string,string>.
  const cases: { name: string; req: ParsedRequest }[] = [
    {
      name: "simple GET",
      req: { method: "GET", url: "https://x.test/a", headers: {}, body: "", insecure: false },
    },
    {
      name: "POST with JSON body",
      req: {
        method: "POST",
        url: "https://x.test/users",
        headers: { "Content-Type": "application/json", Accept: "*/*" },
        body: '{"name":"ada","tags":["a","b"]}',
        insecure: false,
      },
    },
    {
      name: "body containing single quotes",
      req: {
        method: "POST",
        url: "https://x.test/echo",
        headers: {},
        body: `it's a "quoted" value`,
        insecure: false,
      },
    },
    {
      name: "body containing shell metacharacters",
      req: {
        method: "POST",
        url: "https://x.test/echo",
        headers: {},
        body: "$HOME `id` $(whoami) | tee /tmp/x; rm -rf /",
        insecure: false,
      },
    },
    {
      name: "insecure with a query string",
      req: {
        method: "DELETE",
        url: "https://x.test/items?id=1&force=true",
        headers: { "X-Token": "abc:def" },
        body: "",
        insecure: true,
      },
    },
  ];

  for (const c of cases) {
    test(c.name, () => {
      const round = parseCurl(toCurl(c.req));
      assert.deepEqual(round, c.req);
    });
  }
});

describe("substituteVars", () => {
  test("substitutes known names", () => {
    assert.equal(substituteVars("{{host}}/api", { host: "https://x.test" }), "https://x.test/api");
  });

  test("tolerates whitespace inside the braces", () => {
    assert.equal(substituteVars("{{  host  }}", { host: "v" }), "v");
  });

  test("leaves unknown names as literal placeholders", () => {
    // Substituting an empty string would produce a request that looks valid
    // and silently targets the wrong URL; leaving the placeholder makes the
    // mistake visible before it is sent.
    assert.equal(substituteVars("{{missing}}/api", {}), "{{missing}}/api");
  });

  test("substitutes every occurrence", () => {
    assert.equal(substituteVars("{{a}}-{{a}}-{{a}}", { a: "x" }), "x-x-x");
  });

  test("supports dots and dashes in names", () => {
    assert.equal(substituteVars("{{api.base-url}}", { "api.base-url": "v" }), "v");
  });

  test("does not resolve inherited object properties", () => {
    // A var named `constructor` or `toString` must not resolve to something off
    // Object.prototype — hence the hasOwnProperty check in the implementation.
    assert.equal(substituteVars("{{constructor}}", {}), "{{constructor}}");
    assert.equal(substituteVars("{{toString}}", {}), "{{toString}}");
  });

  test("treats replacement values as literal text", () => {
    // A value containing $& or $1 must not be reinterpreted as a regex
    // backreference by String.replace.
    assert.equal(substituteVars("{{v}}", { v: "$& $1 $$" }), "$& $1 $$");
  });

  test("leaves text with no placeholders untouched", () => {
    assert.equal(substituteVars("https://x.test/a?b=c", { unused: "v" }), "https://x.test/a?b=c");
  });
});
