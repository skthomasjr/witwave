"""Unit tests for shared/prompt_env.py (#473)."""

import importlib
import os
import sys
import unittest
from pathlib import Path

# Ensure shared/ is importable regardless of where the test runner is invoked.
_SHARED = Path(__file__).resolve().parents[1] / "shared"
sys.path.insert(0, str(_SHARED))


class PromptEnvTests(unittest.TestCase):
    def setUp(self):
        # Isolated env per-test: nuke the toggles + any test vars.
        for k in [
            "PROMPT_ENV_ENABLED",
            "PROMPT_ENV_ALLOWLIST",
            "PROMPT_ENV_MAX_BYTES",
            "WITWAVE_ENV",
            "WITWAVE_DASHBOARD",
            "WITWAVE_BIG",
            "SECRET_TOKEN",
        ]:
            os.environ.pop(k, None)
        # Reload module so module-level _warned_vars starts fresh.
        import prompt_env  # type: ignore

        importlib.reload(prompt_env)
        self.prompt_env = prompt_env

    def test_disabled_by_default_passes_through(self):
        os.environ["WITWAVE_ENV"] = "staging"
        out = self.prompt_env.resolve_prompt_env("Hello {{env.WITWAVE_ENV}}")
        self.assertEqual(out, "Hello {{env.WITWAVE_ENV}}")

    def test_enabled_with_allowlist_substitutes_matches(self):
        os.environ["PROMPT_ENV_ENABLED"] = "true"
        os.environ["PROMPT_ENV_ALLOWLIST"] = "WITWAVE_"
        os.environ["WITWAVE_ENV"] = "staging"
        os.environ["WITWAVE_DASHBOARD"] = "https://witwave.example.com"
        out = self.prompt_env.resolve_prompt_env("env={{env.WITWAVE_ENV}} dash={{env.WITWAVE_DASHBOARD}}")
        self.assertEqual(out, "env=staging dash=https://witwave.example.com")

    def test_non_allowlisted_var_becomes_empty(self):
        os.environ["PROMPT_ENV_ENABLED"] = "true"
        os.environ["PROMPT_ENV_ALLOWLIST"] = "WITWAVE_"
        os.environ["SECRET_TOKEN"] = "s3cret"
        out = self.prompt_env.resolve_prompt_env("token={{env.SECRET_TOKEN}}")
        self.assertEqual(out, "token=")

    def test_missing_allowlist_var_becomes_empty(self):
        os.environ["PROMPT_ENV_ENABLED"] = "true"
        os.environ["PROMPT_ENV_ALLOWLIST"] = "WITWAVE_"
        out = self.prompt_env.resolve_prompt_env("x={{env.WITWAVE_UNSET}}")
        self.assertEqual(out, "x=")

    def test_glob_pattern_in_allowlist(self):
        os.environ["PROMPT_ENV_ENABLED"] = "true"
        os.environ["PROMPT_ENV_ALLOWLIST"] = "WITWAVE_*,DEPLOY_*"
        os.environ["WITWAVE_ENV"] = "prod"
        os.environ["DEPLOY_TAG"] = "v1.2.3"
        os.environ["SECRET_TOKEN"] = "x"
        out = self.prompt_env.resolve_prompt_env("{{env.WITWAVE_ENV}} {{env.DEPLOY_TAG}} {{env.SECRET_TOKEN}}")
        self.assertEqual(out, "prod v1.2.3 ")

    def test_empty_allowlist_enabled_drops_all(self):
        os.environ["PROMPT_ENV_ENABLED"] = "true"
        os.environ["WITWAVE_ENV"] = "prod"
        out = self.prompt_env.resolve_prompt_env("{{env.WITWAVE_ENV}}")
        self.assertEqual(out, "")

    def test_oversize_truncates_to_cap(self):
        # #1744: PROMPT_ENV_MAX_BYTES caps the post-interpolation body.
        os.environ["PROMPT_ENV_ENABLED"] = "true"
        os.environ["PROMPT_ENV_ALLOWLIST"] = "WITWAVE_"
        os.environ["PROMPT_ENV_MAX_BYTES"] = "32"
        os.environ["WITWAVE_BIG"] = "A" * 200
        out = self.prompt_env.resolve_prompt_env("body={{env.WITWAVE_BIG}}")
        self.assertEqual(len(out.encode("utf-8")), 32)
        # The first 5 bytes should be "body=" — substitution happened
        # before truncation, the cap merely cut the tail.
        self.assertTrue(out.startswith("body="))

    def test_disabled_cap_passes_through(self):
        # PROMPT_ENV_MAX_BYTES=0 disables the cap.
        os.environ["PROMPT_ENV_ENABLED"] = "true"
        os.environ["PROMPT_ENV_ALLOWLIST"] = "WITWAVE_"
        os.environ["PROMPT_ENV_MAX_BYTES"] = "0"
        os.environ["WITWAVE_BIG"] = "X" * 1000
        out = self.prompt_env.resolve_prompt_env("body={{env.WITWAVE_BIG}}")
        self.assertEqual(len(out), len("body=") + 1000)

    def test_oversize_default_cap_pre_resolve_text_is_unchanged(self):
        # When no env-var substitution occurs, the cap is still applied
        # to the resolved (== input) text. A short body must pass through
        # the default 65536 cap unchanged.
        os.environ["PROMPT_ENV_ENABLED"] = "true"
        os.environ["PROMPT_ENV_ALLOWLIST"] = "WITWAVE_"
        out = self.prompt_env.resolve_prompt_env("hello world")
        self.assertEqual(out, "hello world")


if __name__ == "__main__":
    unittest.main()
