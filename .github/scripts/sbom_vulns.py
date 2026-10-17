#!/usr/bin/env python3
"""Add the vulnerabilities of a release to its CycloneDX SBOM (#2086).

sbom_vulns.py SBOM TAG ADVISORIES GRYPE GOVULNCHECK > OUT

SBOM         CycloneDX JSON from syft
TAG          the release tag (v3.1.44)
ADVISORIES   JSON list from GET /repos/{repo}/security-advisories?state=published
             (a draft is never listed there: an unfixed issue is never disclosed)
GRYPE        grype -o json on the SBOM
GOVULNCHECK  govulncheck -format json stream on the source

- metadata.component becomes replication-manager TAG, the ref of our advisories;
- each published advisory affects the product: this release in its vulnerable
  range is affected (exploitable), out of it resolved;
- each grype match affects its component; a Go module vulnerability is exploitable
  when govulncheck finds our code calling it, not_affected (code_not_reachable)
  otherwise; other ecosystems stay in_triage.
"""
import json
import re
import sys

PRODUCT = "replication-manager"
PRODUCT_REF = "pkg:golang/github.com/signal18/replication-manager"


def version_tuple(v):
    return tuple(int(x) for x in re.findall(r"\d+", v.split("-")[0].lstrip("vV"))[:3])


def in_range(version, vrange):
    """vrange as GitHub advisories write it: '< 3.1.43', '>= 3.0.0, < 3.1.43', '= 3.1.2'."""
    if not vrange:
        return True
    v = version_tuple(version)
    for cond in vrange.split(","):
        m = re.match(r"\s*(<=|>=|<|>|=)?\s*v?([\d.]+)", cond)
        if not m:
            continue
        op, bound = m.group(1) or "=", version_tuple(m.group(2))
        ok = {"<": v < bound, "<=": v <= bound, ">": v > bound, ">=": v >= bound, "=": v == bound}[op]
        if not ok:
            return False
    return True


def ratings(severity, cvss=None):
    out = []
    for c in cvss or []:
        if c.get("score"):
            out.append({"score": c["score"], "severity": (severity or "unknown").lower(),
                        "method": c.get("method", "CVSSv31"), "vector": c.get("vector")})
    if not out and severity:
        out.append({"severity": severity.lower()})
    return out


def advisory_vulns(advisories, tag):
    vulns = []
    for a in advisories:
        if a.get("state", "published") != "published":
            continue  # never a draft, whatever the input
        ranges = [v.get("vulnerable_version_range") for v in a.get("vulnerabilities") or []]
        fixed = [v.get("patched_versions") for v in a.get("vulnerabilities") or [] if v.get("patched_versions")]
        affected = any(in_range(tag, r) for r in ranges) if ranges else True
        cvss = []
        v3 = (a.get("cvss_severities") or {}).get("cvss_v3") or {}
        if v3.get("score"):
            cvss.append({"score": v3["score"], "vector": v3.get("vector_string"), "method": "CVSSv31"})
        vuln = {
            "bom-ref": a["ghsa_id"],
            "id": a.get("cve_id") or a["ghsa_id"],
            "source": {"name": "GitHub", "url": a.get("html_url")},
            "references": [{"id": a["ghsa_id"], "source": {"name": "GitHub", "url": a.get("html_url")}}] if a.get("cve_id") else [],
            "ratings": ratings(a.get("severity"), cvss),
            "cwes": [int(cw["cwe_id"].split("-")[1]) for cw in a.get("cwes") or [] if cw.get("cwe_id")],
            "description": a.get("summary"),
            "published": a.get("published_at"),
            "recommendation": f"Upgrade to {', '.join(fixed)} or later" if fixed else None,
            "analysis": {"state": "exploitable" if affected else "resolved"},
            "affects": [{"ref": PRODUCT_REF, "versions": [{"version": tag.lstrip("vV"), "status": "affected" if affected else "unaffected"}]}],
        }
        vulns.append({k: v for k, v in vuln.items() if v not in (None, [])})
    return vulns


def govulncheck_reachable(stream):
    """IDs (GO-..., CVE-..., GHSA-...) of the vulnerabilities our code calls."""
    dec, i, objs = json.JSONDecoder(), 0, []
    while i < len(stream):
        while i < len(stream) and stream[i].isspace():
            i += 1
        if i >= len(stream):
            break
        obj, i = dec.raw_decode(stream, i)
        objs.append(obj)
    aliases = {}
    for o in objs:
        if "osv" in o:
            aliases[o["osv"]["id"]] = set(o["osv"].get("aliases") or [])
    reachable = set()
    for o in objs:
        f = o.get("finding")
        if f and f.get("trace") and f["trace"][0].get("function"):
            reachable.add(f["osv"])
            reachable |= aliases.get(f["osv"], set())
    return reachable, set(aliases) | set().union(*aliases.values()) if aliases else set()


def grype_vulns(grype, components, reachable, scanned_go):
    by_purl = {c.get("purl"): c.get("bom-ref") for c in components if c.get("purl")}
    merged = {}
    for m in grype.get("matches") or []:
        v, art = m["vulnerability"], m["artifact"]
        ids = {v["id"]} | {r["id"] for r in m.get("relatedVulnerabilities") or []}
        ref = by_purl.get(art.get("purl")) or art.get("purl") or art.get("name")
        if art.get("type") == "go-module":
            if ids & reachable:
                analysis = {"state": "exploitable", "detail": "govulncheck: called by replication-manager"}
            elif ids & scanned_go:
                analysis = {"state": "not_affected", "justification": "code_not_reachable",
                            "detail": "govulncheck: the vulnerable code is not called"}
            else:
                analysis = {"state": "in_triage", "detail": "not in the Go vulnerability database"}
        else:
            analysis = {"state": "in_triage"}
        entry = merged.get(v["id"])
        if entry:
            entry["affects"].append({"ref": ref})
            continue
        fixes = (v.get("fix") or {}).get("versions") or []
        cvss = [{"score": c["metrics"]["baseScore"], "vector": c.get("vector"),
                 "method": "CVSSv31" if str(c.get("version", "")).startswith("3") else "other"}
                for c in v.get("cvss") or [] if (c.get("metrics") or {}).get("baseScore")]
        entry = {
            "bom-ref": v["id"],
            "id": v["id"],
            "source": {"name": v.get("namespace", "grype"), "url": v.get("dataSource")},
            "ratings": ratings(v.get("severity"), cvss),
            "description": v.get("description"),
            "recommendation": f"Upgrade {art.get('name')} to {', '.join(fixes)}" if fixes else None,
            "analysis": analysis,
            "affects": [{"ref": ref}],
        }
        merged[v["id"]] = {k: x for k, x in entry.items() if x not in (None, [])}
    return list(merged.values())


def main():
    sbom_path, tag, adv_path, grype_path, gvc_path = sys.argv[1:6]
    sbom = json.load(open(sbom_path))
    meta = sbom.setdefault("metadata", {})
    meta["component"] = {"type": "application", "bom-ref": PRODUCT_REF, "name": PRODUCT,
                         "version": tag.lstrip("vV"), "purl": f"{PRODUCT_REF}@{tag}"}
    reachable, scanned_go = govulncheck_reachable(open(gvc_path).read())
    vulns = advisory_vulns(json.load(open(adv_path)), tag)
    vulns += grype_vulns(json.load(open(grype_path)), sbom.get("components") or [], reachable, scanned_go)
    sbom["vulnerabilities"] = vulns
    json.dump(sbom, sys.stdout, indent=1)
    states = {}
    for v in vulns:
        states[v["analysis"]["state"]] = states.get(v["analysis"]["state"], 0) + 1
    print(f"{len(vulns)} vulnerabilities: {states}", file=sys.stderr)


if __name__ == "__main__":
    main()
