/// <reference types="vite/client" />

// Vite client types (import.meta.env, asset imports, etc.) are included here
// via triple-slash reference instead of tsconfig.json's `types` field so the
// editor doesn't error before `npm install` — the reference directive is a
// soft request (ignored when the target is missing) whereas tsconfig.types
// is a hard requirement.
//
// Vitest globals aren't referenced here because tests use explicit imports
// (`import { describe, it, expect, vi } from "vitest"`) — no globals needed.
