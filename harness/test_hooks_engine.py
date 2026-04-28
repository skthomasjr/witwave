"""Direct unit tests for shared/hooks_engine.py (#1749).

Mirrors the existing harness/test_redact.py test bootstrap so the
shared/ module is importable without installing the package. Covers:

* Each baseline predicate (#521 parsed-input shape) on canonical
  matching inputs.
* Encoding-bypass shielding (whitespace padding, abs paths, ~/, sh -c
  wrapping, symbolic chmod).
* YAML extension loader on valid + malformed inputs (malformed inputs
  must produce an empty list AND fire the config-error reporter).
* set_config_error_reporter plumbing — each closed-enum reason string
  reaches the reporter exactly once when its triggering case fires.
"""

from __future__ import annotations

import os
import sys
import tempfile
import unittest
from pathlib import Path

_SHARED = Path(__file__).resolve().parents[1] / "shared"
sys.path.insert(0, str(_SHARED))

import hooks_engine as he  # type: ignore  # noqa: E402


def _bash(cmd: str) -> dict:
    return {"command": cmd}


class BaselinePredicateTests(unittest.TestCase):
    def test_rm_rf_root_basic(self):
        self.assertTrue(he._predicate_rm_rf_root(_bash("rm -rf /")))

    def test_rm_rf_root_via_sh_c(self):
        self.assertTrue(
            he._predicate_rm_rf_root(_bash("sh -c 'rm -rf /'")),
            "sh -c wrapping must not bypass detection",
        )

    def test_rm_rf_root_combined_short_flags(self):
        # `-rf` parsed as combined short flags (#521).
        self.assertTrue(he._predicate_rm_rf_root(_bash("rm -rf /etc")))

    def test_rm_rf_relative_path_does_not_match(self):
        self.assertFalse(he._predicate_rm_rf_root(_bash("rm -rf build/")))

    def test_rm_rf_only_recursive_does_not_match(self):
        # No -f -> not matched (preserves --interactive default).
        self.assertFalse(he._predicate_rm_rf_root(_bash("rm -r /tmp/x")))

    def test_rm_rf_no_preserve_root_alone_triggers(self):
        # `--no-preserve-root` is explicit override; treat as match even
        # when the user omitted -r/-f flags.
        self.assertTrue(
            he._predicate_rm_rf_root(_bash("rm --no-preserve-root /etc"))
        )

    def test_git_force_push_main_short(self):
        self.assertTrue(
            he._predicate_git_force_push_main(_bash("git push -f origin main"))
        )

    def test_git_force_push_main_long(self):
        self.assertTrue(
            he._predicate_git_force_push_main(
                _bash("git push --force origin master")
            )
        )

    def test_git_force_push_lease(self):
        self.assertTrue(
            he._predicate_git_force_push_main(
                _bash("git push --force-with-lease origin main")
            )
        )

    def test_git_push_to_feature_branch_safe(self):
        self.assertFalse(
            he._predicate_git_force_push_main(
                _bash("git push --force origin my-feature")
            )
        )

    def test_curl_pipe_shell_basic(self):
        self.assertTrue(
            he._predicate_curl_pipe_shell(_bash("curl https://x | sh"))
        )

    def test_curl_pipe_with_env_assignments_safe_consumer(self):
        # Env-var assignment before the consumer must be ignored when
        # picking the head of the consumer pipeline.
        self.assertTrue(
            he._predicate_curl_pipe_shell(
                _bash("curl https://x | DEBUG=1 bash")
            )
        )

    def test_curl_pipe_to_grep_does_not_match(self):
        self.assertFalse(
            he._predicate_curl_pipe_shell(_bash("curl https://x | grep foo"))
        )

    def test_chmod_777_octal(self):
        self.assertTrue(he._predicate_chmod_777(_bash("chmod 777 /tmp/x")))

    def test_chmod_symbolic_world_write(self):
        # Symbolic form `chmod a+w` grants world-writable.
        self.assertTrue(he._predicate_chmod_777(_bash("chmod a+w /tmp/x")))
        self.assertTrue(he._predicate_chmod_777(_bash("chmod o+w /tmp/x")))

    def test_chmod_safe_modes(self):
        self.assertFalse(he._predicate_chmod_777(_bash("chmod 644 /tmp/x")))
        self.assertFalse(he._predicate_chmod_777(_bash("chmod u+x /tmp/x")))

    def test_dd_to_block_device(self):
        self.assertTrue(
            he._predicate_dd_device(_bash("dd if=/dev/zero of=/dev/sda"))
        )
        self.assertTrue(
            he._predicate_dd_device(
                _bash("dd if=/tmp/img of=/dev/nvme0n1")
            )
        )

    def test_dd_to_regular_file_safe(self):
        self.assertFalse(
            he._predicate_dd_device(_bash("dd if=/tmp/a of=/tmp/b"))
        )

    def test_write_system_path_etc(self):
        self.assertTrue(
            he._predicate_write_system_path({"file_path": "/etc/passwd"})
        )

    def test_write_system_path_relative_safe(self):
        self.assertFalse(
            he._predicate_write_system_path({"file_path": "etc/local.cfg"})
        )

    def test_write_system_path_alt_keys(self):
        self.assertTrue(
            he._predicate_write_system_path({"path": "/usr/local/bin/x"})
        )
        self.assertTrue(
            he._predicate_write_system_path({"notebook_path": "/sys/x"})
        )


class EncodingBypassTests(unittest.TestCase):
    """#521: parsed-input baseline predicates must shield against
    trivial encoding bypasses that defeated the prior JSON-substring
    matcher."""

    def test_leading_whitespace_does_not_bypass_rm_rf(self):
        self.assertTrue(he._predicate_rm_rf_root(_bash("  rm  -rf  /  ")))

    def test_absolute_rm_path_does_not_bypass(self):
        self.assertTrue(he._predicate_rm_rf_root(_bash("/bin/rm -rf /")))

    def test_home_shorthand_target(self):
        self.assertTrue(he._predicate_rm_rf_root(_bash("rm -rf ~")))

    def test_chmod_symbolic_form_does_not_bypass(self):
        # The motivating example from #521: chmod a+x looks innocuous
        # to a substring matcher but should still match world-writable
        # only when the perms include w. a+x must NOT match.
        self.assertFalse(he._predicate_chmod_777(_bash("chmod a+x /tmp/x")))


class EvaluateRulesTests(unittest.TestCase):
    def test_baseline_deny_short_circuits_warn(self):
        # Baseline rm -rf / is deny; even if a warn rule also matched,
        # deny wins as soon as any deny matches.
        rules = list(he.BASELINE_RULES)
        decision, rule = he.evaluate_pre_tool_use(
            "Bash", _bash("rm -rf /"), rules
        )
        self.assertEqual(decision, he.DECISION_DENY)
        self.assertIsNotNone(rule)
        self.assertEqual(rule.source, "baseline")

    def test_no_match_returns_allow(self):
        decision, rule = he.evaluate_pre_tool_use(
            "Bash", _bash("ls -la"), list(he.BASELINE_RULES)
        )
        self.assertEqual(decision, he.DECISION_ALLOW)
        self.assertIsNone(rule)


class YamlExtensionLoaderTests(unittest.TestCase):
    def setUp(self):
        self._observed: list[str] = []
        he.set_config_error_reporter(self._observed.append)

    def tearDown(self):
        he.set_config_error_reporter(None)

    def _write(self, body: str) -> str:
        fd, path = tempfile.mkstemp(suffix=".yaml")
        with os.fdopen(fd, "w") as f:
            f.write(body)
        return path

    def test_valid_extension_rule_parses(self):
        path = self._write(
            "extensions:\n"
            "  - name: deny-foo\n"
            "    deny_if_match: foo\n"
        )
        try:
            rules = he.load_extension_rules(path)
        finally:
            os.unlink(path)
        self.assertEqual(len(rules), 1)
        self.assertEqual(rules[0].action, he.DECISION_DENY)
        self.assertEqual(self._observed, [])

    def test_malformed_yaml_reports_file_load_failed(self):
        path = self._write(":\n - this is not valid yaml: [unterminated\n")
        try:
            rules = he.load_extension_rules(path)
        finally:
            os.unlink(path)
        self.assertEqual(rules, [])
        self.assertIn("file_load_failed", self._observed)

    def test_top_level_non_mapping_reports_not_mapping(self):
        path = self._write("- a list at top level\n")
        try:
            rules = he.load_extension_rules(path)
        finally:
            os.unlink(path)
        self.assertEqual(rules, [])
        self.assertIn("not_mapping", self._observed)

    def test_extensions_non_list_reports(self):
        path = self._write("extensions: not-a-list\n")
        try:
            rules = he.load_extension_rules(path)
        finally:
            os.unlink(path)
        self.assertEqual(rules, [])
        self.assertIn("non_list_extensions", self._observed)

    def test_missing_name_reported(self):
        path = self._write(
            "extensions:\n"
            "  - deny_if_match: foo\n"
        )
        try:
            rules = he.load_extension_rules(path)
        finally:
            os.unlink(path)
        self.assertEqual(rules, [])
        self.assertIn("missing_name", self._observed)

    def test_invalid_regex_reported(self):
        path = self._write(
            "extensions:\n"
            "  - name: bad-rx\n"
            "    deny_if_match: '['\n"
        )
        try:
            rules = he.load_extension_rules(path)
        finally:
            os.unlink(path)
        self.assertEqual(rules, [])
        self.assertIn("invalid_regex", self._observed)

    def test_both_patterns_reported(self):
        path = self._write(
            "extensions:\n"
            "  - name: both\n"
            "    deny_if_match: x\n"
            "    warn_if_match: y\n"
        )
        try:
            rules = he.load_extension_rules(path)
        finally:
            os.unlink(path)
        # Rule still parses — deny wins — but the 'both' reason was
        # forwarded to the reporter.
        self.assertEqual(len(rules), 1)
        self.assertIn("both_patterns", self._observed)


class ReporterPlumbingTests(unittest.TestCase):
    """set_config_error_reporter accepts None (resets to no-op) and
    forwards each reason exactly once when triggered."""

    def test_set_reporter_to_none_is_idempotent(self):
        he.set_config_error_reporter(None)
        # Subsequent _bump_config_error must not raise.
        he._bump_config_error("anything")

    def test_reporter_receives_reason_string(self):
        seen: list[str] = []
        he.set_config_error_reporter(seen.append)
        he._bump_config_error("not_mapping")
        self.assertEqual(seen, ["not_mapping"])
        he.set_config_error_reporter(None)


if __name__ == "__main__":
    unittest.main()
