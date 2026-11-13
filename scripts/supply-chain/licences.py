"""licence scan of every go, npm, python and rust dependency in the repository
(the design: everything osi-compatible).

    trivy fs --scanners vuln --list-all-pkgs --format json --output pkgs.json \\
        --skip-dirs resources --skip-dirs dist .
    python3 scripts/supply-chain/licences.py pkgs.json [report.json]

trivy lists every package of go.mod, pnpm-lock.yaml, package-lock.json,
uv.lock and Cargo.lock with the licence it found on disk (module cache,
node_modules). a package trivy found no licence for (an optional platform
binary that is not installed here, or a python or rust package) is looked up
in its registry: npm, pypi or crates.io. nothing is skipped: a package whose
licence cannot be found at all fails the scan.

a licence expression passes when every AND part, and at least one OR
alternative, is on the osi approved list below or in
docs/security/licence-exceptions.json (reviewed non-software licences, each
with a reason and the packages it covers).
"""

import json
import re
import sys
import time
import urllib.parse
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
EXCEPTIONS = ROOT / "docs" / "security" / "licence-exceptions.json"

# spdx ids of licences on the osi approved list (opensource.org/licenses)
OSI = {
    "0BSD", "AFL-3.0", "Apache-2.0", "Artistic-2.0", "BlueOak-1.0.0", "BSD-1-Clause", "BSD-2-Clause",
    "BSD-2-Clause-Patent", "BSD-3-Clause", "BSL-1.0", "CDDL-1.0", "EPL-1.0", "EPL-2.0", "EUPL-1.2",
    "GPL-2.0-only", "GPL-2.0-or-later", "GPL-3.0-only", "GPL-3.0-or-later", "ISC",
    "LGPL-2.1-only", "LGPL-2.1-or-later", "LGPL-3.0-only", "LGPL-3.0-or-later", "MIT", "MIT-0",
    "MPL-2.0", "MS-PL", "NCSA", "OFL-1.1", "PostgreSQL", "Python-2.0", "UPL-1.0", "Unicode-3.0",
    "Unicode-DFS-2016", "Unlicense", "Zlib", "PSF-2.0", "AGPL-3.0-only", "AGPL-3.0-or-later",
}
# plain names some packages use instead of spdx ids
ALIASES = {
    "Apache 2.0": "Apache-2.0", "Apache License 2.0": "Apache-2.0", "Apache-2": "Apache-2.0",
    "Apache License, Version 2.0": "Apache-2.0", "BSD": None, "MIT License": "MIT",
    "BSD License": None, "GPL-3.0": "GPL-3.0-only", "LGPL-3.0": "LGPL-3.0-only",
    "3-Clause BSD License": "BSD-3-Clause", "GPL-2.0": "GPL-2.0-only", "LGPL-2.1": "LGPL-2.1-only", "Python Software Foundation License": "Python-2.0",
}
CLASSIFIERS = {
    "License :: OSI Approved :: MIT License": "MIT",
    "License :: OSI Approved :: BSD License": "BSD-3-Clause",
    "License :: OSI Approved :: Apache Software License": "Apache-2.0",
    "License :: OSI Approved :: ISC License (ISCL)": "ISC",
    "License :: OSI Approved :: Mozilla Public License 2.0 (MPL 2.0)": "MPL-2.0",
    "License :: OSI Approved :: Python Software Foundation License": "Python-2.0",
}


def fetch(url):
    req = urllib.request.Request(url, headers={"User-Agent": "sa-licence-scan", "Accept": "application/json"})
    # transient network errors are retried with a pause; a lookup that
    # still fails stops the scan instead of passing the package
    last = None
    for attempt in range(5):
        try:
            with urllib.request.urlopen(req, timeout=30) as r:
                return json.loads(r.read())
        except Exception as e:
            last = e
            time.sleep(2 * (attempt + 1))
    raise RuntimeError(f"{url}: {last}")


def registry_licence(kind, name, version):
    if kind in ("npm", "pnpm"):
        d = fetch(f"https://registry.npmjs.org/{urllib.parse.quote(name, safe='@')}/{version}")
        lic = d.get("license")
        return lic.get("type") if isinstance(lic, dict) else lic
    if kind == "uv":
        info = fetch(f"https://pypi.org/pypi/{name}/{version}/json")["info"]
        if info.get("license_expression"):
            return info["license_expression"]
        found = [CLASSIFIERS[c] for c in info.get("classifiers") or [] if c in CLASSIFIERS]
        if found:
            return " OR ".join(sorted(set(found)))
        return info.get("license") or None
    if kind == "cargo":
        return fetch(f"https://crates.io/api/v1/crates/{name}/{version}")["version"].get("license")
    return None


def tokens(expr):
    # multi-word aliases become one token before splitting on spaces
    for alias, spdx in ALIASES.items():
        if " " in alias and spdx:
            expr = expr.replace(alias, spdx)
    return re.findall(r"\(|\)|[^\s()]+", expr)


def approved(expr, extra):
    """evaluates an spdx expression; WITH exceptions are accepted for an
    approved base licence, as spdx defines them as additions."""
    toks = tokens(expr.replace("/", " OR "))
    pos = 0

    def atom(t):
        t = ALIASES.get(t, t)
        return t is not None and (t in OSI or t in extra)

    def primary():
        nonlocal pos
        if pos < len(toks) and toks[pos] == "(":
            pos += 1
            v = disj()
            pos += 1  # ")"
            return v
        t = toks[pos]
        pos += 1
        if pos < len(toks) and toks[pos] == "WITH":
            pos += 2
        return atom(t)

    def conj():
        nonlocal pos
        v = primary()
        while pos < len(toks) and toks[pos].upper() == "AND":
            pos += 1
            v = primary() and v
        return v

    def disj():
        nonlocal pos
        v = conj()
        while pos < len(toks) and toks[pos].upper() == "OR":
            pos += 1
            v = conj() or v
        return v

    try:
        return disj()
    except IndexError:
        return False


def main(argv):
    if len(argv) not in (2, 3):
        print(__doc__, file=sys.stderr)
        return 2
    exc = json.loads(EXCEPTIONS.read_text())
    extra = {e["licence"]: set(e["packages"]) for e in exc["exceptions"]}
    doc = json.loads(Path(argv[1]).read_text())
    rows, bad = [], []
    for res in doc["Results"]:
        kind = res.get("Type", "")
        for p in res.get("Packages") or []:
            name, ver = p["Name"], p.get("Version", "")
            lics = p.get("Licenses") or []
            source = "trivy"
            if not lics:
                try:
                    lic = registry_licence(kind, name, ver)
                except RuntimeError as e:
                    print(f"registry lookup failed: {e}", file=sys.stderr)
                    return 2
                lics = [lic] if lic else []
                source = "registry"
            expr = " AND ".join(f"({x})" for x in lics) if lics else ""
            # an exception covers its licence only for the packages it names
            allowed = {lic for lic, pkgs in extra.items() if name in pkgs}
            ok = bool(expr) and approved(expr, allowed)
            rows.append({"target": res["Target"], "package": name, "version": ver, "licence": expr or None, "source": source, "ok": ok})
            if not ok:
                bad.append(rows[-1])
    if len(argv) == 3:
        Path(argv[2]).write_text(json.dumps(rows, indent=1) + "\n")
    counts = {}
    for r in rows:
        counts[r["target"]] = counts.get(r["target"], 0) + 1
    for t, n in sorted(counts.items()):
        print(f"{t}: {n} packages")
    for b in bad:
        print(f"NOT OSI-COMPATIBLE {b['target']}: {b['package']}@{b['version']} licence {b['licence']!r} ({b['source']})")
    print(f"{len(rows)} packages, {len(bad)} failing")
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
