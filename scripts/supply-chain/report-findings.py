"""files the security findings of a run as github issues and sends one alert
for what is new, so a finding is tracked and seen without repeating itself.

    python3 scripts/supply-chain/report-findings.py <findings dir> [--scanned <refs file>]

reads every *.json in the directory: a list of {id, source, subject, pinned,
severity, summary, url}, plus {image, ref, finding} for image findings.

- image findings that docs/security/advisories-reviewed.json lists for the
  image's exact tag (upstream_image_findings) carry a recorded decision and
  are not filed. a new tag is triaged again.
- the remaining findings of one image share one issue, keyed by the image
  repository and edited in place: the title and body always show the
  current findings, and the alert names only the ones the issue did not
  list yet. a finding reported twice in one run is filed once.
- every other finding (an advisory, govulncheck, pnpm audit) gets its own
  issue, keyed by the finding id.
- --scanned names the image references the images job scanned. an open
  image issue whose repository was scanned and has nothing left to file is
  closed with a comment. without the file (the images job failed) no image
  issue is closed, because no answer is not "clean".

the alert goes to ALERT_WEBHOOK_URL (a discord webhook). without it the
script exits 1 after filing, so the run is red and github's own failure
notice still reaches the maintainers.
"""

import json
import os
import re
import subprocess
import sys
import urllib.request
from pathlib import Path

LABEL = "security-finding"
ROOT = Path(__file__).resolve().parents[2]
REVIEWED = ROOT / "docs" / "security" / "advisories-reviewed.json"
KEY_RE = re.compile(r"<!-- security-finding-key: (.+?) -->")
LISTED_RE = re.compile(r"<!-- listed: (.*?) -->")
SEVERITY = ["unknown", "low", "medium", "high", "critical"]


def gh(*args):
    return subprocess.run(["gh", *args], check=True, capture_output=True, text=True).stdout


def repo_of(image):
    # registry/name:tag -> registry/name; a registry port has no slash after it
    name, sep, tag = image.rpartition(":")
    return name if sep and "/" not in tag else image


def decided(reviewed):
    out = {}
    for e in reviewed.get("upstream_image_findings", []):
        out.setdefault(e["image"], set()).update(e["findings"])
    return out


def group(findings, decisions):
    groups, skipped = {}, 0
    for f in findings:
        if f.get("image"):
            if f["finding"] in decisions.get(f["image"], ()):
                skipped += 1
                continue
            key = f"image {repo_of(f['image'])}"
            g = groups.setdefault(key, {"image": f["image"], "items": {}})
            g["items"].setdefault(f["finding"], f)
        else:
            groups.setdefault(f"id {f['id']}", {"items": {}})["items"].setdefault(f["id"], f)
    return groups, skipped


def render(key, g):
    items = sorted(g["items"].values(), key=lambda f: (-SEVERITY.index(f["severity"]) if f["severity"] in SEVERITY else 0, f["id"]))
    top = max((f["severity"] for f in items), key=lambda s: SEVERITY.index(s) if s in SEVERITY else 0)
    listed = " | ".join(sorted(g["items"]))
    marks = f"<!-- security-finding-key: {key} -->\n<!-- listed: {listed} -->\n"
    if "image" in g:
        title = f"[{key}] {len(items)} {top} finding{'s' if len(items) != 1 else ''} at {g['image']}"
        rows = "\n".join(
            f"| {f['severity']} | {f['finding']} | {f['pinned']} | {f['summary']} | {f['url']} |" for f in items)
        body = (
            f"{marks}fixable critical and high findings of `{g['image']}` without a recorded decision.\n\n"
            "| severity | finding | installed | summary | link |\n|---|---|---|---|---|\n"
            f"{rows}\n\n"
            "this issue is updated in place by the hourly security workflow and closed when the image "
            "has no finding left without a decision. bump the pin where a release fixes a finding; "
            "otherwise record the decision in docs/security/advisories-reviewed.json "
            "(upstream_image_findings) and docs/security/supply-chain.md section 8.1. "
            "triage within the 24 hour window: docs/runbooks/security-patch.md"
        )
    else:
        f = items[0]
        title = f"[{f['id']}] {f['severity']} {f['subject']} {f['pinned']}"
        body = (
            f"{marks}source: {f['source']}\n\nsubject: {f['subject']} {f['pinned']}\n\n"
            f"severity: {f['severity']}\n\n{f['summary']}\n\n{f['url']}\n\n"
            "triage within the 24 hour window: docs/runbooks/security-patch.md"
        )
    return title, body


def main(argv):
    args = argv[1:]
    scanned = None
    if len(args) == 3 and args[1] == "--scanned":
        p = Path(args[2])
        if p.exists():
            scanned = {repo_of(line.split("@")[0]) for line in p.read_text().split() if line}
        args = args[:1]
    if len(args) != 1:
        print(__doc__, file=sys.stderr)
        return 2
    findings = []
    for f in sorted(Path(args[0]).glob("*.json")):
        findings.extend(json.loads(f.read_text()))
    groups, skipped = group(findings, decided(json.loads(REVIEWED.read_text())))
    print(f"{len(findings)} findings, {skipped} with a recorded decision, {len(groups)} to file")

    gh("label", "create", LABEL, "--color", "B60205", "--description", "security workflow finding", "--force")
    issues = json.loads(gh("issue", "list", "--label", LABEL, "--state", "open", "--limit", "500",
                           "--json", "number,title,body,url"))
    by_key = {}
    for i in issues:
        m = KEY_RE.search(i["body"] or "")
        if m:
            by_key.setdefault(m.group(1), i)
    # issues filed before the key marker existed, one per finding id
    legacy_titles = " ".join(i["title"] for i in issues if not KEY_RE.search(i["body"] or ""))

    new = []
    for key, g in sorted(groups.items()):
        title, body = render(key, g)
        cur = by_key.get(key)
        if cur:
            m = LISTED_RE.search(cur["body"] or "")
            before = set(m.group(1).split(" | ")) if m and m.group(1) else set()
            added = [g["items"][k] for k in sorted(set(g["items"]) - before)]
            if cur["title"] != title or cur["body"] != body:
                gh("issue", "edit", str(cur["number"]), "--title", title, "--body", body)
                print(f"updated {cur['url']}")
            new.extend((f, cur["url"]) for f in added)
            continue
        if key.startswith("id ") and f"[{key[3:]}]" in legacy_titles:
            continue
        url = gh("issue", "create", "--label", LABEL, "--title", title, "--body", body).strip()
        print(f"opened {url}")
        new.extend((f, url) for f in g["items"].values())

    if scanned is not None:
        for key, i in by_key.items():
            if key.startswith("image ") and key not in groups and key[6:] in scanned:
                gh("issue", "close", str(i["number"]), "--comment",
                   "the latest scan has no finding of this image left without a recorded decision "
                   "(docs/security/advisories-reviewed.json, upstream_image_findings).")
                print(f"closed {i['url']}")

    if not new:
        print("nothing new to alert")
        return 0
    hook = os.environ.get("ALERT_WEBHOOK_URL", "")
    if not hook:
        print("ALERT_WEBHOOK_URL is not set: issues filed, no alert sent", file=sys.stderr)
        return 1
    text = "security findings:\n" + "\n".join(f"- [{f['id']}] {f['severity']} {f['subject']}: {u}" for f, u in new)
    req = urllib.request.Request(hook, data=json.dumps({"content": text[:1900]}).encode(),
                                 headers={"Content-Type": "application/json"}, method="POST")
    with urllib.request.urlopen(req, timeout=30):
        pass
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
