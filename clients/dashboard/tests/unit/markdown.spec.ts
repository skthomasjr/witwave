import { describe, expect, it } from "vitest";
import { renderMarkdown } from "../../src/utils/markdown";

// #1604: regression guard for the DOMPurify `afterSanitizeAttributes` hook
// installed at module load in src/utils/markdown.ts. The hook is process-
// global state — any future module that calls `DOMPurify.removeAllHooks()`,
// or that ships its own conflicting hook, would silently strip the
// `target`/`rel` attributes the dashboard relies on for phishing
// mitigation (#527). These tests fail loudly when that happens.

describe("renderMarkdown link hardening (#527, #1604)", () => {
  it("forces target=_blank and rel=noopener noreferrer on external https links", () => {
    const html = renderMarkdown("[external link](https://example.com)");
    expect(html).toContain('href="https://example.com"');
    expect(html).toContain('target="_blank"');
    expect(html).toContain('rel="noopener noreferrer"');
  });

  it("forces target=_blank and rel=noopener noreferrer on external http links", () => {
    const html = renderMarkdown("[plain http](http://example.com)");
    expect(html).toContain('target="_blank"');
    expect(html).toContain('rel="noopener noreferrer"');
  });

  it("leaves relative links untouched (no forced target/rel)", () => {
    const html = renderMarkdown("[relative](/dashboard/path)");
    expect(html).toContain('href="/dashboard/path"');
    expect(html).not.toContain('target="_blank"');
    expect(html).not.toContain("noopener noreferrer");
  });

  it("strips javascript: scheme via DOMPurify default allow-list", () => {
    const html = renderMarkdown("[xss](javascript:alert(1))");
    expect(html).not.toContain("javascript:");
  });
});
