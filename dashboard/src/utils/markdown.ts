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

export function renderMarkdown(source: string | null | undefined): string {
  const text = (source ?? "").trim();
  if (!text) return "";
  // marked.parse returns string | Promise<string>; with async=false it's
  // synchronous, so we cast rather than await in a render path.
  const raw = marked.parse(text, { async: false }) as string;
  return DOMPurify.sanitize(raw);
}
