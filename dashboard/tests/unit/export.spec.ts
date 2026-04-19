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
});

describe("timestamped", () => {
  it("returns prefix-YYYYMMDD-HHMMSS.ext", () => {
    const out = timestamped("nyx-conversations", "csv");
    expect(out).toMatch(/^nyx-conversations-\d{8}-\d{6}\.csv$/);
  });
});
