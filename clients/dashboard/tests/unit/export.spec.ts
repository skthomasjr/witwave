import { describe, expect, it } from "vitest";
import { csvEscape, timestamped } from "../../src/utils/export";

describe("csvEscape", () => {
  it("returns plain text unquoted", () => {
    expect(csvEscape("hello")).toBe("hello");
  });

  it("quotes values containing commas", () => {
    expect(csvEscape("a,b")).toBe('"a,b"');
  });

  it("doubles embedded quotes", () => {
    expect(csvEscape('he said "hi"')).toBe('"he said ""hi"""');
  });

  it("quotes values containing newlines", () => {
    expect(csvEscape("line1\nline2")).toBe('"line1\nline2"');
  });

  it("quotes leading/trailing whitespace", () => {
    expect(csvEscape(" leading")).toBe('" leading"');
    expect(csvEscape("trailing ")).toBe('"trailing "');
  });

  it("serialises non-strings as JSON", () => {
    expect(csvEscape({ a: 1 })).toBe('"{""a"":1}"');
  });

  it("emits empty string for null/undefined", () => {
    expect(csvEscape(null)).toBe("");
    expect(csvEscape(undefined)).toBe("");
  });

  // #1732: formula-injection neutralisation. Spreadsheet apps interpret
  // a cell whose first character is one of `=`, `+`, `-`, `@`, tab, CR,
  // LF as a formula. We prepend a single apostrophe so the cell is
  // treated as literal text. Newline/CR-leading cells additionally need
  // RFC 4180 quoting on top.
  it("prefixes apostrophe to cells starting with formula sigils", () => {
    expect(csvEscape("=HYPERLINK(\"http://x/\", \"y\")")).toBe(
      '"\'=HYPERLINK(""http://x/"", ""y"")"',
    );
    expect(csvEscape("+1+1")).toBe("'+1+1");
    expect(csvEscape("-1+1")).toBe("'-1+1");
    expect(csvEscape("@SUM(A1:A2)")).toBe("'@SUM(A1:A2)");
  });

  it("prefixes apostrophe to cells starting with tab/CR/LF", () => {
    // Tab-leading: apostrophe inserted; tab is not by itself an
    // RFC-4180 quoting trigger so the cell stays unquoted.
    expect(csvEscape("\t=cmd|'/c calc'!A1")).toBe("'\t=cmd|'/c calc'!A1");
    // CR/LF-leading: apostrophe inserted AND the embedded line break
    // forces RFC-4180 quoting on top.
    expect(csvEscape("\r=foo")).toBe('"\'\r=foo"');
    expect(csvEscape("\n=foo")).toBe('"\'\n=foo"');
  });

  it("does not alter benign cells that merely contain a sigil", () => {
    // Sigils only matter at the START of the cell.
    expect(csvEscape("a=b")).toBe("a=b");
    expect(csvEscape("ok+go")).toBe("ok+go");
    expect(csvEscape("agent@host")).toBe("agent@host");
  });
});

describe("timestamped", () => {
  it("returns prefix-YYYYMMDD-HHMMSS.ext", () => {
    const out = timestamped("witwave-conversations", "csv");
    expect(out).toMatch(/^witwave-conversations-\d{8}-\d{6}\.csv$/);
  });
});
