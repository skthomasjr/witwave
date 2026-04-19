// Extend vitest's expect with vitest-axe's toHaveNoViolations matcher.
// Loaded via vite.config.ts -> test.setupFiles.
import { expect } from "vitest";
import * as matchers from "vitest-axe/matchers";

expect.extend(matchers);

// Module augmentation so vue-tsc accepts `expect(results).toHaveNoViolations()`.
// vitest-axe's bundled d.ts augments the legacy `Vi` namespace; modern
// vitest's Assertion type is re-exported through `vitest` itself, so we
// augment that module here.
declare module "vitest" {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  interface Assertion<T = any> {
    toHaveNoViolations(): void;
  }
  interface AsymmetricMatchersContaining {
    toHaveNoViolations(): void;
  }
}
