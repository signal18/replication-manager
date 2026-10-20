"""python3 -m unittest .github/scripts/test_sbom_vulns.py (from .github/scripts)"""
import unittest

from sbom_vulns import advisory_vulns, govulncheck_reachable, in_range


class TestSbomVulns(unittest.TestCase):
    def test_in_range(self):
        self.assertTrue(in_range("v3.1.42", "< 3.1.43"))
        self.assertFalse(in_range("v3.1.43", "<3.1.43"))
        self.assertTrue(in_range("3.1.10", ">= 3.1.0, < 3.1.43"))
        self.assertFalse(in_range("3.0.32", ">= 3.1.0, < 3.1.43"))
        self.assertTrue(in_range("3.1.2", "= 3.1.2"))

    def test_advisory_status_and_draft(self):
        adv = {"ghsa_id": "GHSA-x", "state": "published", "severity": "high", "summary": "s",
               "vulnerabilities": [{"vulnerable_version_range": "< 3.1.43", "patched_versions": "3.1.43"}]}
        draft = dict(adv, ghsa_id="GHSA-draft", state="draft")
        old = advisory_vulns([adv, draft], "v3.1.42")
        self.assertEqual([v["id"] for v in old], ["GHSA-x"], "a draft is never published")
        self.assertEqual(old[0]["analysis"]["state"], "exploitable")
        self.assertEqual(advisory_vulns([adv], "v3.1.44")[0]["analysis"]["state"], "resolved")

    def test_govulncheck_reachable(self):
        stream = '{"osv": {"id": "GO-1", "aliases": ["CVE-1"]}}\n{"osv": {"id": "GO-2"}}\n' \
                 '{"finding": {"osv": "GO-1", "trace": [{"module": "m", "function": "F"}]}}\n' \
                 '{"finding": {"osv": "GO-2", "trace": [{"module": "m"}]}}'
        reachable, scanned = govulncheck_reachable(stream)
        self.assertEqual(reachable, {"GO-1", "CVE-1"})
        self.assertEqual(scanned, {"GO-1", "GO-2", "CVE-1"})


if __name__ == "__main__":
    unittest.main()
