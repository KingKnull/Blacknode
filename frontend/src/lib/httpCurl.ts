// Helpers for cURL <-> saved-request interop and {{var}} substitution.
//
// parseCurl is "good enough for the common shapes" — it handles `-X`,
// `-H`, `--header`, `-d`/`--data*`, `-k`/`--insecure`, line continuations,
// and single/double quoting. It is *not* a full POSIX shell parser; weird
// constructs like `$(...)` or env interpolation will pass through as raw
// text.

export type ParsedRequest = {
  method: string;
  url: string;
  headers: Record<string, string>;
  body: string;
  insecure: boolean;
};

// Tokenize a shell-ish command string. Handles single + double quotes and
// backslash escapes inside double quotes. Newlines outside of quotes are
// treated as whitespace so multi-line `\`-joined cURL pastes work.
function tokenize(input: string): string[] {
  const out: string[] = [];
  let cur = "";
  let i = 0;
  let quote: '"' | "'" | null = null;
  while (i < input.length) {
    const c = input[i]!;
    if (quote === "'") {
      if (c === "'") {
        quote = null;
      } else {
        cur += c;
      }
      i++;
      continue;
    }
    if (quote === '"') {
      if (c === "\\" && i + 1 < input.length) {
        cur += input[i + 1];
        i += 2;
        continue;
      }
      if (c === '"') {
        quote = null;
      } else {
        cur += c;
      }
      i++;
      continue;
    }
    if (c === "'" || c === '"') {
      quote = c as '"' | "'";
      i++;
      continue;
    }
    if (c === "\\" && input[i + 1] === "\n") {
      // Line continuation — skip both characters.
      i += 2;
      continue;
    }
    if (c === "\\" && i + 1 < input.length) {
      cur += input[i + 1];
      i += 2;
      continue;
    }
    if (c === " " || c === "\t" || c === "\n" || c === "\r") {
      if (cur !== "") {
        out.push(cur);
        cur = "";
      }
      i++;
      continue;
    }
    cur += c;
    i++;
  }
  if (cur !== "") out.push(cur);
  return out;
}

export function parseCurl(input: string): ParsedRequest {
  const tokens = tokenize(input.trim());
  if (tokens.length === 0 || tokens[0] !== "curl") {
    throw new Error("input must start with `curl`");
  }
  let method = "";
  let url = "";
  const headers: Record<string, string> = {};
  let body = "";
  let insecure = false;

  for (let i = 1; i < tokens.length; i++) {
    const t = tokens[i]!;
    if (t === "-X" || t === "--request") {
      method = (tokens[++i] ?? "").toUpperCase();
    } else if (t === "-H" || t === "--header") {
      const raw = tokens[++i] ?? "";
      const idx = raw.indexOf(":");
      if (idx > 0) {
        headers[raw.slice(0, idx).trim()] = raw.slice(idx + 1).trim();
      }
    } else if (
      t === "-d" ||
      t === "--data" ||
      t === "--data-raw" ||
      t === "--data-binary" ||
      t === "--data-ascii"
    ) {
      body = tokens[++i] ?? "";
      if (!method) method = "POST";
    } else if (t === "-k" || t === "--insecure") {
      insecure = true;
    } else if (t === "--url") {
      url = tokens[++i] ?? "";
    } else if (t === "-G" || t === "--get") {
      method = "GET";
    } else if (t === "-I" || t === "--head") {
      method = "HEAD";
    } else if (t === "--compressed" || t === "-L" || t === "--location") {
      // Recognized but irrelevant to the saved-request model.
      continue;
    } else if (t.startsWith("-")) {
      // Skip the flag and (best-effort) its argument if it doesn't look
      // like another flag. This avoids losing the URL when a cURL paste
      // includes options we don't model.
      const next = tokens[i + 1];
      if (next && !next.startsWith("-") && !looksLikeURL(next)) {
        i++;
      }
    } else if (!url && looksLikeURL(t)) {
      url = t;
    }
  }
  if (!method) method = "GET";
  if (!url) throw new Error("no URL found in curl line");
  return { method, url, headers, body, insecure };
}

function looksLikeURL(s: string): boolean {
  return /^https?:\/\//i.test(s) || s.startsWith("/") || s.includes("://");
}

// Render a saved request as a copy-pastable cURL command. Multi-line with
// `\` continuations so it reads cleanly when shared.
export function toCurl(opts: {
  method: string;
  url: string;
  headers: Record<string, string>;
  body: string;
  insecure: boolean;
}): string {
  const lines: string[] = [`curl -X ${opts.method} ${shellQuote(opts.url)}`];
  for (const [k, v] of Object.entries(opts.headers)) {
    if (!k) continue;
    lines.push(`  -H ${shellQuote(`${k}: ${v}`)}`);
  }
  if (opts.body) {
    lines.push(`  --data ${shellQuote(opts.body)}`);
  }
  if (opts.insecure) {
    lines.push(`  --insecure`);
  }
  return lines.join(" \\\n");
}

function shellQuote(s: string): string {
  // Single-quote and escape any embedded single quote with the standard
  // `'\''` trick. Safe for every printable byte and nicer than POSIX
  // double-quoting which would interpret $ and backticks.
  return `'${s.replace(/'/g, "'\\''")}'`;
}

// Apply {{varName}} substitution. Unknown names are left untouched so the
// user sees the literal placeholder and can fix it before sending.
export function substituteVars(
  text: string,
  vars: Record<string, string>,
): string {
  return text.replace(/\{\{\s*([\w.-]+)\s*\}\}/g, (m, name) => {
    if (Object.prototype.hasOwnProperty.call(vars, name)) {
      return vars[name];
    }
    return m;
  });
}
