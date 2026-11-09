#!/usr/bin/env python3
"""Tests for pinned_contract_gate.py. Run: python3 scripts/harness/test_pinned_contract_gate.py"""
import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(__file__))
import pinned_contract_gate as gate  # noqa: E402

PIN = {"local": "contracts/x.yaml", "repo": "IQVO/x", "path": "apis/asyncapi.yaml", "ref": "develop"}


class CompareTest(unittest.TestCase):
    def test_equal_text_reports_nothing(self):
        self.assertEqual(gate.compare(PIN, "a\nb\n", "a\nb\n"), [])

    def test_differing_text_reports_a_stale_pin(self):
        out = gate.compare(PIN, "a\n", "b\n")
        self.assertEqual(out[0], "STALE PIN contracts/x.yaml")
        self.assertIn("IQVO/x apis/asyncapi.yaml on develop", out[1])

    def test_trailing_newline_counts(self):
        self.assertNotEqual(gate.compare(PIN, "a", "a\n"), [])

    def test_long_diff_is_truncated(self):
        out = gate.compare(PIN, "\n".join(str(i) for i in range(100)), "\n".join(str(i + 1000) for i in range(100)))
        self.assertTrue(any(line.startswith("... ") and line.endswith("more diff lines") for line in out))
        self.assertLessEqual(len(out), 3 + 30 + 1)


if __name__ == "__main__":
    unittest.main()
