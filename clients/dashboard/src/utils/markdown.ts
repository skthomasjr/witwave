import { marked } from "marked";
import DOMPurify from "dompurify";

// Matches the legacy ui/ pipeline (marked → DOMPurify) so agent-card
// descriptions render identically. We keep the call behind a helper so every
// consumer goes through the same sanitization pass — never touch innerHTML
// with raw markdown elsewhere.

marked.setOptions({
  gfm: true,
  breaks: false,
});

// Security (#527): force rel="noopener noreferrer" and target="_blank" on
// every external <a> emitted by agent markdown. Agent output is untrusted;
// DOMPurify's default profile blocks scripts but leaves link rel/target
// unchanged, which lets a compromised or hallucinating backend phish
// operators via `window.opener`-style redirect vectors. `javascript:` /
// `data:` schemes are already stripped by DOMPurify's default allow-list —
// this hook does not re-enable them.
let hookInstalled = false;
function installLinkHook(): void {
  if (hookInstalled) return;
  DOMPurify.addHook("afterSanitizeAttributes", (node) => {
    if (!(node instanceof Element)) return;
    if (node.tagName !== "A") return;
    const href = node.getAttribute("href") ?? "";
    // Only force _blank / rel=noopener on external http(s) links. Leave
    // in-app relative links (if any ever emerge) with their native target.
    const isExternal = /^https?:\/\//i.test(href);
    if (!isExternal) return;
    node.setAttribute("target", "_blank");
    node.setAttribute("rel", "noopener noreferrer");
  });
  hookInstalled = true;
}
installLinkHook();

export function renderMarkdown(source: string | null | undefined): string {
  const text = (source ?? "").trim();
  if (!text) return "";
  // marked.parse returns string | Promise<string>; with async=false it's
  // synchronous, so we cast rather than await in a render path.
  const raw = marked.parse(text, { async: false }) as string;
  return DOMPurify.sanitize(raw, { ADD_ATTR: ["target", "rel"] });
}
