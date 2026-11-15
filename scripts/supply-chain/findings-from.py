"""turns scanner output into the finding list report-findings.py reads.

    python3 scripts/supply-chain/findings-from.py trivy <image ref> <trivy json> > f.json
    python3 scripts/supply-chain/findings-from.py govulncheck <module dir> <govulncheck -format json output> > f.json
    python3 scripts/supply-chain/findings-from.py pnpm-audit <pnpm audit --json output> > f.json

trivy: critical and high findings with a fixed version only (the release gate's
policy), one per vulnerability and package, with the image named so the
report groups them per image. govulncheck: vulnerabilities with a call path into our code
(symbol level), which are the ones govulncheck itself fails on. pnpm audit:
high and critical advisories. finding ids are stable across runs, so an open
issue is not opened again every hour.
"""

import json
import sys


def trivy(ref, path):
    # one finding per vulnerability and package: an image that ships the
    # same module in several binaries (grafana's bundled plugins) reports it
    # once, with every installed version
    doc = json.load(open(path))
    image = ref.split("@")[0]
    found = {}
    for res in doc.get("Results") or []:
        for v in res.get("Vulnerabilities") or []:
            if v.get("Severity") in ("CRITICAL", "HIGH") and v.get("FixedVersion"):
                key = f"{v['VulnerabilityID']} {v['PkgName']}"
                f = found.setdefault(key, {
                    "id": f"{key} in {image}", "image": image, "ref": ref, "finding": key,
                    "source": "trivy", "subject": v["PkgName"], "versions": set(),
                    "severity": v["Severity"].lower(), "title": v.get("Title", ""),
                    "fixed": v["FixedVersion"], "url": v.get("PrimaryURL", ""),
                })
                f["versions"].add(v.get("InstalledVersion", ""))
                if v["Severity"] == "CRITICAL":
                    f["severity"] = "critical"
    out = []
    for f in found.values():
        f["pinned"] = ", ".join(sorted(f.pop("versions")))
        f["summary"] = f"{f.pop('title')} fixed in {f.pop('fixed')} ({ref})"
        out.append(f)
    return out


def govulncheck(mod, path):
    # the json output is a stream of objects; findings with a function in
    # the trace's first frame are called from our code
    osv, called = {}, {}
    dec = json.JSONDecoder()
    text = open(path).read()
    i = 0
    while i < len(text):
        while i < len(text) and text[i].isspace():
            i += 1
        if i >= len(text):
            break
        obj, i = dec.raw_decode(text, i)
        if "osv" in obj:
            osv[obj["osv"]["id"]] = obj["osv"]
        f = obj.get("finding")
        if f and f.get("trace") and f["trace"][0].get("function"):
            called[f["osv"]] = f
    out = []
    for vid, f in called.items():
        o = osv.get(vid, {})
        out.append({
            "id": f"{vid} in {mod}", "source": "govulncheck", "subject": f["trace"][0].get("module", ""),
            "pinned": f["trace"][0].get("version", ""), "severity": "unknown",
            "summary": f"{o.get('summary', '')} fixed in {f.get('fixed_version', 'unknown')}",
            "url": f"https://pkg.go.dev/vuln/{vid}",
        })
    return out


def pnpm_audit(path):
    doc = json.load(open(path))
    out = []
    for adv in (doc.get("advisories") or {}).values():
        if adv.get("severity") in ("high", "critical"):
            out.append({
                "id": f"{adv.get('github_advisory_id') or adv.get('id')} {adv['module_name']}",
                "source": "pnpm audit", "subject": adv["module_name"],
                "pinned": ",".join(sorted({f.get("version", "") for f in adv.get("findings", [])})),
                "severity": adv["severity"],
                "summary": f"{adv.get('title', '')} patched in {adv.get('patched_versions', 'unknown')}",
                "url": adv.get("url", ""),
            })
    return out


def main(argv):
    if len(argv) == 4 and argv[1] == "trivy":
        out = trivy(argv[2], argv[3])
    elif len(argv) == 4 and argv[1] == "govulncheck":
        out = govulncheck(argv[2], argv[3])
    elif len(argv) == 3 and argv[1] == "pnpm-audit":
        out = pnpm_audit(argv[2])
    else:
        print(__doc__, file=sys.stderr)
        return 2
    json.dump(out, sys.stdout, indent=2)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
