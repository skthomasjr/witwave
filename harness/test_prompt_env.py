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
            "WITWAVE_ENV",
            "WITWAVE_DASHBOARD",
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
        out = self.prompt_env.resolve_prompt_env(
            "env={{env.WITWAVE_ENV}} dash={{env.WITWAVE_DASHBOARD}}"
        )
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
        out = self.prompt_env.resolve_prompt_env(
            "{{env.WITWAVE_ENV}} {{env.DEPLOY_TAG}} {{env.SECRET_TOKEN}}"
        )
        self.assertEqual(out, "prod v1.2.3 ")

    def test_empty_allowlist_enabled_drops_all(self):
        os.environ["PROMPT_ENV_ENABLED"] = "true"
        os.environ["WITWAVE_ENV"] = "prod"
        out = self.prompt_env.resolve_prompt_env("{{env.WITWAVE_ENV}}")
        self.assertEqual(out, "")


if __name__ == "__main__":
    unittest.main()
