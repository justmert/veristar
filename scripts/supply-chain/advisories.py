"""hourly upstream advisory check (docs/runbooks/security-patch.md).

reports every upstream security signal for the pinned versions, so detection
does not use up the 24 hour patch window:

  - umami: every published repository advisory of umami-software/umami that
    is not listed as reviewed, and any umami release newer than the pin. umami
    has published advisories without a fixed version, so the check never reads
    "fixed in" or "affected" wording: an unreviewed advisory id is a finding by
    itself, and a newer release is a finding by itself.
  - next, react and react-dom at the exact versions in the pinned umami
    release's pnpm-lock.yaml, nats-server, caddy, the opentelemetry collector
    and haproxy's and etcd's go modules where they exist: github's global
    advisory database queried with affects=<package>@<version>, which matches
    on the advisory's version ranges.
  - postgresql: a newer minor release of the pinned major on postgresql.org.
    every postgresql minor release carries security fixes, so a newer minor
    is a finding without reading release notes.

    python3 scripts/supply-chain/advisories.py [--json out.json]

GITHUB_TOKEN (or GH_TOKEN) is used for the github api when set. exit status:
0 no findings, 1 findings, 2 the check itself failed (a source could not be
read). a failed source is never treated as "no findings".
"""

import json
import os
import re
import sys
import urllib.parse
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
PINS = ROOT / "infra" / "ansible" / "vars" / "pins.yml"
PG_DOCKERFILE = ROOT / "deploy" / "images" / "postgres-ha.Dockerfile"
GO_MOD = ROOT / "go.mod"
REVIEWED = ROOT / "docs" / "security" / "advisories-reviewed.json"
CADDY_DOCKERFILE = ROOT / "deploy" / "images" / "caddy.Dockerfile"


class SourceError(Exception):
    pass


def get(url, accept="application/json", github=False):
    req = urllib.request.Request(url, headers={"Accept": accept, "User-Agent": "sa-advisory-check"})
    tok = os.environ.get("GITHUB_TOKEN") or os.environ.get("GH_TOKEN")
    if github and tok:
        req.add_header("Authorization", f"Bearer {tok}")
    if github:
        req.add_header("X-GitHub-Api-Version", "2022-11-28")
    # three attempts for transient network errors; a source that still fails
    # fails the whole check
    last = None
    for _ in range(3):
        try:
            with urllib.request.urlopen(req, timeout=30) as r:
                return r.read()
        except Exception as e:
            last = e
    raise SourceError(f"{url}: {last}") from last


def gh_json(path):
    return json.loads(get("https://api.github.com" + path, "application/vnd.github+json", github=True))


def pinned_umami():
    m = re.search(r"umami-software/umami:(\d+\.\d+\.\d+)@", PINS.read_text())
    if not m:
        raise SourceError("no umami pin in vars/pins.yml")
    return m.group(1)


def pinned_go(module):
    m = re.search(rf"^\s*{re.escape(module)} (v\S+)", GO_MOD.read_text(), re.M)
    return m.group(1) if m else None


def lock_versions(tag, names):
    text = get(f"https://raw.githubusercontent.com/umami-software/umami/v{tag}/pnpm-lock.yaml", "text/plain").decode()
    out = {}
    for n in names:
        # package keys of the lock's packages section, e.g. "  next@16.4.2:"
        found = sorted(set(re.findall(rf"^  {re.escape(n)}@(\d+\.\d+\.\d+[^:(\s]*):", text, re.M)))
        if not found:
            raise SourceError(f"{n} not found in umami {tag} pnpm-lock.yaml")
        out[n] = found
    return out


def global_advisories(ecosystem, package, version):
    q = urllib.parse.urlencode({"ecosystem": ecosystem, "affects": f"{package}@{version}", "per_page": 100})
    return gh_json(f"/advisories?{q}")


def main(argv):
    out_path = None
    if len(argv) == 3 and argv[1] == "--json":
        out_path = argv[2]
    elif len(argv) != 1:
        print(__doc__, file=sys.stderr)
        return 2

    reviewed = json.loads(REVIEWED.read_text())
    reviewed_ids = {a["id"] for a in reviewed["umami_repository_advisories"]}
    findings = []
    try:
        umami = pinned_umami()

        # umami repository advisories, wording-independent
        for a in gh_json("/repos/umami-software/umami/security-advisories?per_page=100&state=published"):
            if a["ghsa_id"] not in reviewed_ids:
                findings.append({
                    "id": a["ghsa_id"], "source": "umami repository advisory",
                    "subject": "umami", "pinned": umami, "severity": a.get("severity") or "unknown",
                    "summary": a.get("summary", ""), "url": a.get("html_url", ""),
                })

        # a newer umami release
        rel = gh_json("/repos/umami-software/umami/releases/latest")
        latest = rel["tag_name"].lstrip("v")
        if tuple(map(int, latest.split("."))) > tuple(map(int, umami.split("."))):
            findings.append({
                "id": f"umami-release-{latest}", "source": "umami release",
                "subject": "umami", "pinned": umami, "severity": "unknown",
                "summary": f"umami {latest} is released, pinned is {umami}", "url": rel.get("html_url", ""),
            })

        # next and react as built into the pinned umami
        checks = []
        for name, versions in lock_versions(umami, ["next", "react", "react-dom"]).items():
            for v in versions:
                checks.append(("npm", name, v, f"umami {umami}"))

        # our own pins of upstream servers
        nats = re.search(r"nats:(\d+\.\d+\.\d+)-alpine@", PINS.read_text())
        if not nats:
            raise SourceError("no nats pin in vars/pins.yml")
        checks.append(("go", "github.com/nats-io/nats-server/v2", "v" + nats.group(1), "nats image"))
        caddy = re.search(r"xcaddy build (v\d+\.\d+\.\d+)", CADDY_DOCKERFILE.read_text())
        if not caddy:
            raise SourceError("no caddy version in caddy.Dockerfile")
        checks.append(("go", "github.com/caddyserver/caddy/v2", caddy.group(1), "caddy image"))
        etcd = re.search(r"etcd:(v\d+\.\d+\.\d+)@", PINS.read_text())
        if not etcd:
            raise SourceError("no etcd pin in vars/pins.yml")
        checks.append(("go", "go.etcd.io/etcd/server/v3", etcd.group(1), "etcd image"))
        for mod in ["go.opentelemetry.io/collector/pdata", "go.opentelemetry.io/collector/component"]:
            v = pinned_go(mod)
            if not v:
                raise SourceError(f"{mod} not in go.mod")
            checks.append(("go", mod, v, "collector"))

        for eco, pkg, ver, where in checks:
            for a in global_advisories(eco, pkg, ver):
                if a.get("withdrawn_at"):
                    continue
                findings.append({
                    "id": a["ghsa_id"], "source": f"github advisory database ({where})",
                    "subject": pkg, "pinned": ver, "severity": a.get("severity") or "unknown",
                    "summary": a.get("summary", ""), "url": a.get("html_url", ""),
                })

        # postgresql: a newer minor of the pinned major
        pg = re.search(r"^ARG PG_VERSION=(\d+)\.(\d+)$", PG_DOCKERFILE.read_text(), re.M)
        if not pg:
            raise SourceError("no PG_VERSION in postgres-ha.Dockerfile")
        major, minor = pg.group(1), int(pg.group(2))
        rows = [v for v in json.loads(get("https://www.postgresql.org/versions.json")) if str(v["major"]) == major]
        if not rows:
            raise SourceError(f"postgresql {major} missing from versions.json")
        latest_minor = int(rows[0]["latestMinor"])
        if latest_minor > minor:
            findings.append({
                "id": f"postgresql-{major}.{latest_minor}", "source": "postgresql.org versions",
                "subject": "postgresql", "pinned": f"{major}.{minor}", "severity": "unknown",
                "summary": f"postgresql {major}.{latest_minor} is released (every minor release carries security fixes)",
                "url": "https://www.postgresql.org/support/security/",
            })
    except SourceError as e:
        print(f"advisory check failed: {e}", file=sys.stderr)
        return 2

    # one finding per id, the first source wins
    seen, unique = set(), []
    for f in findings:
        if f["id"] not in seen:
            seen.add(f["id"])
            unique.append(f)
    for f in unique:
        print(f"FINDING {f['id']} [{f['severity']}] {f['subject']} {f['pinned']}: {f['summary']} ({f['source']}) {f['url']}")
    if out_path:
        Path(out_path).write_text(json.dumps(unique, indent=2) + "\n")
    if not unique:
        print("no upstream findings for the pinned versions")
    return 1 if unique else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
