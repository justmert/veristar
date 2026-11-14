"""opens one labelled github issue per new security finding and sends one
alert for the run, so a finding is tracked and seen.

    python3 scripts/supply-chain/report-findings.py <findings dir>

reads every *.json in the directory: a list of {id, source, subject, pinned,
severity, summary, url}. an issue whose title carries the finding id and that
is still open is not opened again, so an hourly run does not repeat itself.
the alert goes to ALERT_WEBHOOK_URL (a discord webhook). without it
the script exits 1 after opening the issues, so the run is red and github's
own failure notice still reaches the maintainers.
"""

import json
import os
import subprocess
import sys
import urllib.request
from pathlib import Path

LABEL = "security-finding"


def gh(*args):
    return subprocess.run(["gh", *args], check=True, capture_output=True, text=True).stdout


def main(argv):
    if len(argv) != 2:
        print(__doc__, file=sys.stderr)
        return 2
    findings = []
    for f in sorted(Path(argv[1]).glob("*.json")):
        findings.extend(json.loads(f.read_text()))
    if not findings:
        print("no findings")
        return 0

    gh("label", "create", LABEL, "--color", "B60205", "--description", "security workflow finding", "--force")
    open_titles = json.loads(gh("issue", "list", "--label", LABEL, "--state", "open", "--limit", "500", "--json", "title"))
    titles = " ".join(t["title"] for t in open_titles)
    opened = []
    for f in findings:
        if f"[{f['id']}]" in titles:
            continue
        title = f"[{f['id']}] {f['severity']} {f['subject']} {f['pinned']}"
        body = (
            f"source: {f['source']}\n\nsubject: {f['subject']} {f['pinned']}\n\n"
            f"severity: {f['severity']}\n\n{f['summary']}\n\n{f['url']}\n\n"
            "triage within the 24 hour window: docs/runbooks/security-patch.md"
        )
        url = gh("issue", "create", "--label", LABEL, "--title", title, "--body", body).strip()
        opened.append((f, url))
        print(f"opened {url}")

    if not opened:
        print("every finding already has an open issue")
        return 0
    hook = os.environ.get("ALERT_WEBHOOK_URL", "")
    if not hook:
        print("ALERT_WEBHOOK_URL is not set: issues opened, no alert sent", file=sys.stderr)
        return 1
    text = "security findings:\n" + "\n".join(f"- [{f['id']}] {f['severity']} {f['subject']}: {u}" for f, u in opened)
    req = urllib.request.Request(hook, data=json.dumps({"content": text[:1900]}).encode(),
                                 headers={"Content-Type": "application/json"}, method="POST")
    with urllib.request.urlopen(req, timeout=30):
        pass
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
