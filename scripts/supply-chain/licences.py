"""licence gate (the design: everything we ship is osi-compatible).

    go mod download all
    go run go.opentelemetry.io/collector/cmd/builder@v0.165.0 \\
        --config collector/builder-config.yaml --skip-compilation --skip-strict-versioning=false
    (cd dist/scf-otelcol && go mod download all)
    trivy fs --scanners vuln --list-all-pkgs --format json --output pkgs.json \\
        --skip-dirs resources --skip-dirs dist .
    trivy fs --scanners vuln --list-all-pkgs --format json --output otelcol.json dist/scf-otelcol
    node apps/console/scripts/bundle-packages.mjs > console-bundle.txt
    python3 -I scripts/supply-chain/licences.py pkgs.json --otelcol otelcol.json \\
        --console-bundle console-bundle.txt [--report report.json]

every package trivy lists (go.mod, pnpm-lock.yaml, the example lockfiles,
uv.lock, Cargo.lock, requirements.txt, the generated collector module) lands
in exactly one of two sections, using docs/security/licence-policy.json:

- shipped: go modules linked into a binary of a platform image (`go list
  -deps` of the policy's go packages) and npm packages reachable through
  `dependencies` and `optionalDependencies` from a shipped workspace importer
  in pnpm-lock.yaml (plus the bundled dev packages the policy names). any
  licence that is not osi approved fails. there are no exceptions in this
  section. every package of the real console bundle must be in it.
- not shipped: dev, test, example and tooling dependencies. they are listed
  with the importers that pull them in. a non-osi licence fails unless the
  policy's allowlist has an entry for that exact name, version and licence
  with a written reason. an allowlist entry that no longer matches a
  not-shipped non-osi package (removed, upgraded, relicensed or now shipped)
  also fails, so the allowlist cannot go stale silently.

a package without a licence on disk is looked up in its registry (npm, pypi,
crates.io); a lookup that fails stops the scan. a package that cannot be
placed in either section fails the scan.
"""

import json
import posixpath
import re
import subprocess
import sys
import time
import urllib.parse
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
POLICY = ROOT / "docs" / "security" / "licence-policy.json"
OWN_MODULE = "github.com/justmert/veristar"

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
    "License :: OSI Approved :: GNU General Public License v3 or later (GPLv3+)": "GPL-3.0-or-later",
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
    if kind in ("uv", "pip"):
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


def approved(expr):
    """evaluates an spdx expression; WITH exceptions are accepted for an
    approved base licence, as spdx defines them as additions."""
    toks = tokens(expr.replace("/", " OR "))
    pos = 0

    def atom(t):
        t = ALIASES.get(t, t)
        return t is not None and t in OSI

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


# pnpm-lock.yaml (lockfile v9). python's standard library has no yaml parser
# and the gate must not install one, so this reads the two sections it needs
# from pnpm's fixed, machine-written layout and refuses anything else.

def unquote(s):
    s = s.strip()
    if len(s) >= 2 and s[0] == s[-1] == "'":
        return s[1:-1].replace("''", "'")
    if len(s) >= 2 and s[0] == s[-1] == '"':
        return s[1:-1]
    return s


def split_key(line):
    # "  'a@1(b@2)': x" or "  a@1: x": the key ends at the first colon outside quotes
    s = line.strip()
    if s.startswith("'"):
        i = 1
        while True:
            i = s.index("'", i)
            if i + 1 < len(s) and s[i + 1] == "'":
                i += 2
                continue
            break
        return unquote(s[: i + 1]), s[i + 2 :].strip()
    k, _, v = s.partition(":")
    return k.strip(), v.strip()


def parse_pnpm_lock(path):
    """returns (importers, snapshots). importers maps an importer path to
    {"dependencies"|"devDependencies"|"optionalDependencies"|
    "packageManagerDependencies": {name: version}}; snapshots maps a
    snapshot key to {name: version} of its runtime dependencies."""
    importers, snapshots = {}, {}
    section = None
    cur = group = name = None
    for raw in path.read_text().splitlines():
        if not raw.strip() or raw.lstrip().startswith("#"):
            continue
        indent = len(raw) - len(raw.lstrip(" "))
        if raw == "---":
            section = None
            continue
        if indent == 0:
            key = raw.split(":", 1)[0]
            section = key if key in ("importers", "snapshots") else None
            continue
        if section is None:
            continue
        if indent == 2:
            k, v = split_key(raw)
            cur, group, name = k, None, None
            if section == "importers":
                # the package manager document declares importer "." again;
                # its groups are merged into the same importer
                importers.setdefault(k, {})
            else:
                if v not in ("", "{}"):
                    raise ValueError(f"unexpected snapshot line: {raw!r}")
                snapshots.setdefault(k, {})
            continue
        if indent == 4:
            k, v = split_key(raw)
            group = k
            if section == "importers":
                importers[cur].setdefault(k, {})
            continue
        if section == "importers" and indent == 6:
            name, _ = split_key(raw)
            continue
        if section == "importers" and indent == 8:
            k, v = split_key(raw)
            if k == "version":
                importers[cur][group][name] = unquote(v)
            continue
        if section == "snapshots" and indent == 6 and group in ("dependencies", "optionalDependencies"):
            k, v = split_key(raw)
            snapshots[cur][k] = unquote(v)
            continue
        if section == "snapshots" and indent == 6:
            continue  # transitivePeerDependencies and the like
        raise ValueError(f"unexpected pnpm-lock.yaml layout: {raw!r}")
    return importers, snapshots


def base(key):
    # "@scope/name@1.2.3(peer@4)" -> ("@scope/name", "1.2.3")
    k = key.split("(", 1)[0]
    i = k.rindex("@")
    return k[:i], k[i + 1 :]


def resolve(name, version):
    # an alias (npm:pkg@ver) is stored as a full snapshot key
    head = version.split("(", 1)[0]
    if "@" in head[1:]:
        return version
    return f"{name}@{version}"


def pnpm_reach(importers, snapshots):
    """returns {(name, version): {"shipped": set(labels), "other": set(labels)}}.
    a shipped importer contributes its runtime groups, plus the dev packages
    the policy names as bundled; every other group, and every group of a
    not-shipped importer, is recorded as not shipped with its label."""
    policy = json.loads(POLICY.read_text())
    shipped_importers = policy["shipped"]["pnpm_importers"]
    for imp, spec in shipped_importers.items():
        if imp not in importers:
            raise ValueError(f"shipped importer {imp} is not in pnpm-lock.yaml")
        for n in spec.get("also_bundled", {}):
            if n not in importers[imp].get("devDependencies", {}):
                raise ValueError(f"{imp}: also_bundled {n} is not one of its devDependencies")
    reach = {}

    def walk(start, imp_dir, label, shipped, files_only):
        seen, stack = set(), list(start.items())
        while stack:
            n, v = stack.pop()
            if v.startswith("link:"):
                # a workspace package: follow its runtime dependencies
                target = posixpath.normpath(posixpath.join(imp_dir, v[5:]))
                if ("link", target) in seen:
                    continue
                seen.add(("link", target))
                if target not in importers:
                    raise ValueError(f"{label}: link {v} resolves to unknown importer {target}")
                for g in ("dependencies", "optionalDependencies"):
                    stack.extend(importers[target].get(g, {}).items())
                continue
            key = resolve(n, v)
            if key in seen:
                continue
            seen.add(key)
            if key not in snapshots:
                raise ValueError(f"{label}: {key} has no snapshot in pnpm-lock.yaml")
            name = base(key)[0]
            r = reach.setdefault(base(key), {"shipped": set(), "other": set()})
            (r["shipped"] if shipped else r["other"]).add(label)
            if shipped and name in files_only:
                # only a file of this package is bundled; its own
                # dependencies are recorded below as not shipped
                continue
            stack.extend(snapshots[key].items())

    for imp, groups in importers.items():
        spec = shipped_importers.get(imp)
        for g, deps in groups.items():
            runtime = g in ("dependencies", "optionalDependencies")
            if spec is None:
                walk(deps, imp, f"{imp} {g}", False, {})
                continue
            files_only = spec.get("files_only", {})
            if runtime:
                walk(deps, imp, f"{imp} {g}", True, files_only)
                walk(deps, imp, f"{imp} {g} (not bundled)", False, {})
            else:
                bundled = {n: v for n, v in deps.items() if n in spec.get("also_bundled", {})}
                walk(bundled, imp, f"{imp} {g} (bundled)", True, files_only)
                walk(deps, imp, f"{imp} {g}", False, {})
    return reach


def go_shipped():
    """module path@version of every module linked into a shipped go binary."""
    policy = json.loads(POLICY.read_text())
    mods = set()
    for entry in policy["shipped"]["go"]:
        d = ROOT / entry["dir"]
        if not (d / "go.mod").exists():
            raise ValueError(f"{entry['dir']}/go.mod is missing ({entry['artifact']}); generate it first")
        out = subprocess.run(
            ["go", "list", "-deps", "-f", "{{with .Module}}{{.Path}}@{{.Version}}{{end}}", *entry["packages"]],
            cwd=d, capture_output=True, text=True,
        )
        if out.returncode != 0:
            raise ValueError(f"go list in {entry['dir']} failed: {out.stderr.strip()}")
        mods.update(line for line in out.stdout.split() if line)
    return mods


def classify(target, kind, pkg, pnpm, gomods):
    """returns ("shipped" | "not-shipped" | "own", why)."""
    rel = pkg.get("Relationship")
    target = target.replace("\\", "/")
    if rel == "root" or pkg["Name"] == OWN_MODULE or pkg["Name"].startswith(OWN_MODULE + "/"):
        return "own", "this repository's own code (Apache-2.0, LICENSE)"
    if kind == "gomod":
        key = f"{pkg['Name']}@{pkg.get('Version', '')}"
        if target in ("go.mod", "dist/scf-otelcol/go.mod") and key in gomods:
            return "shipped", f"linked into a platform binary ({target})"
        if target == "examples/otel-go-nethttp/go.mod":
            return "not-shipped", "example app"
        if target in ("go.mod", "dist/scf-otelcol/go.mod"):
            return "not-shipped", f"{target}: test or tool module, not linked into a shipped binary"
        return None, f"unknown go target {target}"
    if kind == "pnpm":
        if target != "pnpm-lock.yaml":
            return None, f"unknown pnpm target {target}"
        r = pnpm.get((pkg["Name"], pkg.get("Version", "")))
        if r is None:
            return None, "not reachable from any importer in pnpm-lock.yaml"
        if r["shipped"]:
            return "shipped", "runtime dependency of " + ", ".join(sorted(r["shipped"]))
        return "not-shipped", "pulled in by " + ", ".join(sorted(r["other"]))
    if target.startswith("examples/"):
        return "not-shipped", "example app"
    if target == "infra/ansible/requirements.txt":
        return "not-shipped", "operator tooling (ansible controller)"
    return None, f"unknown target {target}"


def main(argv):
    args = argv[1:]
    opts = {}
    for flag in ("--report", "--otelcol", "--console-bundle"):
        if flag in args:
            i = args.index(flag)
            opts[flag] = args[i + 1]
            del args[i : i + 2]
    report = opts.get("--report")
    if len(args) != 1 or "--otelcol" not in opts or "--console-bundle" not in opts:
        print(__doc__, file=sys.stderr)
        return 2
    policy = json.loads(POLICY.read_text())
    allow = {(a["package"], a["version"]): a for a in policy["not_shipped_allowlist"]}
    for a in policy["not_shipped_allowlist"]:
        if not a.get("reason", "").strip() or not a.get("licence"):
            print(f"allowlist entry {a['package']}@{a['version']} has no licence or reason", file=sys.stderr)
            return 2
    try:
        importers, snapshots = parse_pnpm_lock(ROOT / "pnpm-lock.yaml")
        pnpm = pnpm_reach(importers, snapshots)
        gomods = go_shipped()
    except ValueError as e:
        print(f"cannot classify dependencies: {e}", file=sys.stderr)
        return 2

    rows = []
    seen_targets = set()
    # the collector scan runs inside dist/scf-otelcol, so its targets are
    # relative to that directory
    for f, prefix in ((args[0], ""), (opts["--otelcol"], "dist/scf-otelcol/")):
        for res in json.loads(Path(f).read_text())["Results"]:
            kind = res.get("Type", "")
            target = prefix + res["Target"]
            seen_targets.add(target)
            for p in res.get("Packages") or []:
                name, ver = p["Name"], p.get("Version", "")
                section, why = classify(target, kind, p, pnpm, gomods)
                lics = p.get("Licenses") or []
                source = "trivy"
                if section != "own" and not lics:
                    try:
                        lic = registry_licence(kind, name, ver)
                    except RuntimeError as e:
                        print(f"registry lookup failed: {e}", file=sys.stderr)
                        return 2
                    lics = [lic] if lic else []
                    source = "registry"
                expr = " AND ".join(f"({x})" for x in lics) if lics else None
                rows.append({
                    "target": target, "package": name, "version": ver, "licence": expr,
                    "source": source, "section": section, "why": why,
                    "osi": bool(expr) and approved(expr),
                })
    for t in ("go.mod", "dist/scf-otelcol/go.mod", "pnpm-lock.yaml"):
        if t not in seen_targets:
            print(f"trivy output has no {t}; the gate needs it", file=sys.stderr)
            return 2

    bad = []
    unplaced = [r for r in rows if r["section"] is None]
    shipped = [r for r in rows if r["section"] == "shipped"]
    other = [r for r in rows if r["section"] == "not-shipped"]
    own = [r for r in rows if r["section"] == "own"]
    used = set()

    print(f"== shipped code and its production dependencies: {len(shipped)} packages")
    for r in shipped:
        if not r["osi"]:
            bad.append(r)
            print(f"FAIL shipped {r['target']}: {r['package']}@{r['version']} licence {r['licence']!r} ({r['source']}); {r['why']}")

    print(f"== not shipped (dev, test, example, tooling): {len(other)} packages")
    for r in other:
        if r["osi"]:
            continue
        a = allow.get((r["package"], r["version"]))
        if a is None:
            bad.append(r)
            print(f"FAIL not allowlisted {r['target']}: {r['package']}@{r['version']} licence {r['licence']!r} ({r['source']}); {r['why']}")
        elif a["licence"] != (r["licence"] or "none"):
            bad.append(r)
            print(f"FAIL allowlist licence changed {r['package']}@{r['version']}: reviewed {a['licence']!r}, now {r['licence']!r}")
        else:
            used.add((r["package"], r["version"]))
            print(f"allowlisted {r['package']}@{r['version']} licence {r['licence']!r}; {r['why']}")
            print(f"    reason: {a['reason']}")
    stale = sorted(set(allow) - used)
    for n, v in stale:
        print(f"FAIL stale allowlist entry {n}@{v}: no longer a not-shipped package with that non-osi licence")

    # every package the console bundle really contains must be in the shipped
    # section, so the lockfile model above cannot under-report
    shipped_keys = {(r["package"], r["version"]) for r in shipped if r["target"] == "pnpm-lock.yaml"}
    bundle = [line.strip() for line in Path(opts["--console-bundle"]).read_text().splitlines() if line.strip()]
    if not bundle:
        print("console bundle list is empty", file=sys.stderr)
        return 2
    missing = []
    for item in bundle:
        n, _, v = item.rpartition("@")
        if (n, v) not in shipped_keys:
            missing.append(item)
            print(f"FAIL in the console bundle but not in the shipped section: {item}")
    print(f"== console bundle: {len(bundle)} packages, {len(missing)} outside the shipped section")

    for r in unplaced:
        print(f"FAIL unclassified {r['target']}: {r['package']}@{r['version']}: {r['why']}")

    print(f"== own modules: {len(own)} ({', '.join(sorted({r['package'] for r in own}))})")
    counts = {}
    for r in rows:
        counts[r["target"]] = counts.get(r["target"], 0) + 1
    for t, n in sorted(counts.items()):
        print(f"{t}: {n} packages")
    nfail = len(bad) + len(stale) + len(unplaced) + len(missing)
    print(f"{len(rows)} packages: {len(shipped)} shipped, {len(other)} not shipped "
          f"({len(used)} allowlisted), {len(own)} own; {nfail} failing")
    if report:
        Path(report).write_text(json.dumps(rows, indent=1) + "\n")
    return 1 if nfail else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
