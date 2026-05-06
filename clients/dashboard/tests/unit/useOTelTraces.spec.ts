import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// Unit tests for useOTelTraces validators (#1704). Targets the
// load-bearing CSP / scheme allow-list gates that previously had no
// direct coverage — OTelTracesView.spec.ts mocks fetch globally so
// the validators never fire.
//
// We import the test-only handles `__validateTraceBaseUrl` and
// `__isSameOrigin` exposed by the composable for this exact purpose
// (#1704 — keeps the validator surface visible to tests without
// changing the public composable contract).

function freshImport() {
  vi.resetModules();
  return import("../../src/composables/useOTelTraces");
}

function setLocationOrigin(origin: string): void {
  // jsdom's window.location is read-only by default; redefine via
  // Object.defineProperty so each test can simulate a different host.
  Object.defineProperty(window, "location", {
    configurable: true,
    writable: true,
    value: new URL(origin),
  });
}

describe("useOTelTraces / validateTraceBaseUrl", () => {
  let warnSpy: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    setLocationOrigin("https://dashboard.witwave.example");
    warnSpy = vi.spyOn(console, "warn").mockImplementation(() => {});
  });

  afterEach(() => {
    warnSpy.mockRestore();
  });

  // ----- protocol allow-list -----

  it("rejects file:// URLs", async () => {
    const mod = await freshImport();
    expect(mod.__validateTraceBaseUrl("file:///etc/passwd", true)).toBeNull();
    expect(warnSpy).toHaveBeenCalled();
  });

  it("rejects javascript: URLs", async () => {
    const mod = await freshImport();
    expect(mod.__validateTraceBaseUrl("javascript:alert(1)", true)).toBeNull();
  });

  it("rejects data: URLs", async () => {
    const mod = await freshImport();
    expect(mod.__validateTraceBaseUrl("data:text/html,<script>", true)).toBeNull();
  });

  it("rejects gopher:// URLs", async () => {
    const mod = await freshImport();
    expect(mod.__validateTraceBaseUrl("gopher://evil.com/", true)).toBeNull();
  });

  // ----- malformed URLs -----

  it("rejects non-URL garbage", async () => {
    const mod = await freshImport();
    expect(mod.__validateTraceBaseUrl("not-a-url-at-all", true)).toBeNull();
    expect(warnSpy).toHaveBeenCalled();
  });

  it("rejects empty / null / undefined input", async () => {
    const mod = await freshImport();
    expect(mod.__validateTraceBaseUrl("", true)).toBeNull();
    expect(mod.__validateTraceBaseUrl(null, true)).toBeNull();
    expect(mod.__validateTraceBaseUrl(undefined, true)).toBeNull();
  });

  // ----- trailing-slash stripping -----

  it("strips trailing slashes from accepted URLs", async () => {
    const mod = await freshImport();
    expect(mod.__validateTraceBaseUrl("https://dashboard.witwave.example/api////", true)).toBe(
      "https://dashboard.witwave.example/api",
    );
  });

  // ----- cross-origin gate -----

  it("rejects cross-origin URL when allowCrossOrigin=false", async () => {
    const mod = await freshImport();
    expect(mod.__validateTraceBaseUrl("https://other.example/jaeger", false)).toBeNull();
    expect(warnSpy).toHaveBeenCalled();
  });

  it("accepts cross-origin URL when allowCrossOrigin=true", async () => {
    const mod = await freshImport();
    expect(mod.__validateTraceBaseUrl("https://other.example/jaeger", true)).toBe("https://other.example/jaeger");
  });

  it("accepts same-origin URL even when allowCrossOrigin=false", async () => {
    const mod = await freshImport();
    expect(mod.__validateTraceBaseUrl("https://dashboard.witwave.example/api/traces", false)).toBe(
      "https://dashboard.witwave.example/api/traces",
    );
  });

  it("rejects http URL when current origin is https (different origin)", async () => {
    const mod = await freshImport();
    expect(mod.__validateTraceBaseUrl("http://dashboard.witwave.example/api", false)).toBeNull();
  });

  // ----- isSameOrigin direct -----

  it("isSameOrigin accepts URL with matching origin", async () => {
    const mod = await freshImport();
    expect(mod.__isSameOrigin(new URL("https://dashboard.witwave.example/x"))).toBe(true);
  });

  it("isSameOrigin rejects URL with different host", async () => {
    const mod = await freshImport();
    expect(mod.__isSameOrigin(new URL("https://other.example/x"))).toBe(false);
  });

  it("isSameOrigin rejects URL with different port", async () => {
    const mod = await freshImport();
    expect(mod.__isSameOrigin(new URL("https://dashboard.witwave.example:8443/x"))).toBe(false);
  });

  it("isSameOrigin rejects URL with different scheme", async () => {
    const mod = await freshImport();
    expect(mod.__isSameOrigin(new URL("http://dashboard.witwave.example/x"))).toBe(false);
  });

  // ----- security defense: window.location.origin missing -----

  it("isSameOrigin returns false when window.location.origin is missing (fail-closed)", async () => {
    Object.defineProperty(window, "location", {
      configurable: true,
      writable: true,
      value: { origin: undefined },
    });
    const mod = await freshImport();
    // Even a URL with the same string-shape as the missing origin
    // should not be accepted — fail-closed defends against fixture
    // / harness misconfig.
    expect(mod.__isSameOrigin(new URL("https://anywhere.example/x"))).toBe(false);
  });

  it("isSameOrigin returns false when window.location access throws", async () => {
    // Some configurations make window.location a getter that throws
    // (very restrictive jsdom variants). The validator must fail-
    // closed in that case.
    Object.defineProperty(window, "location", {
      configurable: true,
      get() {
        throw new Error("access denied");
      },
    });
    const mod = await freshImport();
    expect(mod.__isSameOrigin(new URL("https://anywhere.example/x"))).toBe(false);
  });
});
