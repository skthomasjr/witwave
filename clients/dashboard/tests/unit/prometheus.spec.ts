import { describe, expect, it } from "vitest";
import { parseProm } from "../../src/utils/prometheus";

describe("parseProm", () => {
  it("parses a basic counter sample", () => {
    const out = parseProm(["# HELP foo help", "# TYPE foo counter", 'foo_total{a="1"} 42'].join("\n"));
    const fam = out.get("foo");
    expect(fam).toBeDefined();
    expect(fam!.samples[0].labels).toEqual({ a: "1" });
    expect(fam!.samples[0].value).toBe(42);
  });

  it("handles label values containing right-brace (#1009)", () => {
    const out = parseProm('foo_total{msg="timeout: rpc }"} 1');
    const fam = out.get("foo");
    expect(fam!.samples).toHaveLength(1);
    expect(fam!.samples[0].labels.msg).toBe("timeout: rpc }");
    expect(fam!.samples[0].value).toBe(1);
  });

  it("handles escaped double-quote in label values (#1009)", () => {
    const out = parseProm('foo_total{q="a\\"b"} 3');
    const fam = out.get("foo");
    expect(fam!.samples[0].labels.q).toBe('a"b');
    expect(fam!.samples[0].value).toBe(3);
  });

  it("handles escaped backslash in label values (#1009)", () => {
    const out = parseProm('foo_total{p="c:\\\\x"} 7');
    const fam = out.get("foo");
    expect(fam!.samples[0].labels.p).toBe("c:\\x");
  });

  it("handles multiple labels with mixed escapes", () => {
    const out = parseProm('foo_total{a="x",b="y\\"z",c="w"} 5');
    const fam = out.get("foo");
    expect(fam!.samples[0].labels).toEqual({ a: "x", b: 'y"z', c: "w" });
  });

  it("skips malformed sample lines", () => {
    const out = parseProm('foo_total{unterminated="oops 1');
    expect(out.get("foo")).toBeUndefined();
  });
});
