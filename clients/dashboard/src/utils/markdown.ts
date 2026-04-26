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
// #1394 KNOWN RESIDUAL RISK: DOMPurify hooks are process-global. Any
// future module that registers its own hook — or test that calls
// removeAllHooks() — changes the behaviour of every renderMarkdown()
// caller (6 v-html sites in AgentCard / ConversationsView / ChatPanel /
// ConversationDrawer / AgentDetail). Defensive posture for now:
// (a) hookInstalled gate prevents re-registration from this module;
// (b) CSP blocks scripts + restricts img-src, belt-and-braces;
// (c) DOMPurify's default allow-list already strips `javascript:` /
//     `data:` schemes.
// A proper fix would switch to a per-call `sanitize(..., { HOOKS: ... })`
// form when DOMPurify exposes it without a global addHook call. Until
// then, any new DOMPurify consumer in this codebase must audit
// their interaction with this hook — `installLinkHook` must not be
// undone by `DOMPurify.removeAllHooks()`.
// #1604 GLOBAL-STATE REGRESSION GUARD: tests/unit/markdown.spec.ts asserts
// that renderMarkdown() emits target/rel attributes on external links. If
// any future module calls `DOMPurify.removeAllHooks()` (or registers a
// conflicting hook that strips these attributes), that test fails loudly
// rather than silently regressing the phishing mitigation above.
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
