#!/usr/bin/env python3
"""replaces every generated secret of one environment, for rotate-all.yml
(docs/handover/checklist.md):

    infra/secrets/rotate-secrets.py <env> [--age <recipient>[,<recipient>...]]

1. decrypts infra/secrets/<env>/secrets.enc.yaml with the runner's age
   identity (SOPS_AGE_KEY_FILE),
2. writes the current values, unchanged, to secrets.previous.enc.yaml,
3. writes new random values for every generated key to secrets.enc.yaml,
   the same way new-secrets.py makes them,
4. encrypts both files to the environment's recipients: the creation rule
   of infra/secrets/.sops.yaml (staging, production), or the recipients
   given with --age (the local converge, which has no rule),
5. re-encrypts the private network's ca.enc.yaml and tls.enc.yaml
   (playbooks/internal-ca.yml) to the same recipients, values unchanged:
   every play reads tls.enc.yaml with the new identity, so a file left on the
   old recipients would stop rotate-all.yml at its first node.

rotate-all.yml reads both files, applies the new values, and proves every
previous value is refused. delete secrets.previous.enc.yaml after it passed:
it holds credentials that no longer open anything, but it is still a list of
them.

two kinds of value are not generated here:
- pgbackrest_cipher_pass is kept: the backup repository is encrypted with it
  and cannot be re-keyed in place (docs/runbooks/secret-rotation.md). a new
  environment built for a handover has its own repository and value.
  report_signing_seed and report_signing_key are kept too: the key's
  account id is published and named by every report already shared, so it
  is replaced on its own with a retirement step (the same runbook).
- provider-issued values (github oauth, azure, cloudflare, discord) come
  from their providers. a new value is taken from the same SA_* variables
  new-secrets.py reads; a value without one keeps its value, and
  rotate-all.yml then names it as not rotated.

both plaintexts exist only in memory and in sops's stdin.
"""

import argparse
import os
import subprocess
import sys

import yaml

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import importlib.util  # noqa: E402

_spec = importlib.util.spec_from_file_location(
    "new_secrets", os.path.join(os.path.dirname(os.path.abspath(__file__)), "new-secrets.py")
)
new_secrets = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(new_secrets)

# the report key pair is published: every report already shared names its
# key, so a new pair is a deliberate step of its own
# (docs/runbooks/secret-rotation.md, "The usage report signing key")
KEPT = {"pgbackrest_cipher_pass", "report_signing_seed", "report_signing_key"}


def sops(args, stdin=None):
    binary = os.environ.get("SOPS", "sops")
    r = subprocess.run([binary, *args], input=stdin, capture_output=True, check=False)
    if r.returncode != 0:
        sys.stderr.write(r.stderr.decode())
        raise SystemExit(f"rotate-secrets.py: sops {args[0]} failed")
    return r.stdout


def encrypt(doc, path, root, recipients):
    plain = yaml.safe_dump(doc, sort_keys=False).encode()
    if recipients:
        args = ["encrypt", "--input-type", "yaml", "--output-type", "yaml", "--age", recipients]
    else:
        # the creation rule of the file's path picks the recipients
        args = ["--config", os.path.join(root, "infra/secrets/.sops.yaml"), "encrypt",
                "--input-type", "yaml", "--output-type", "yaml", "--filename-override", path]
    out = sops(args + ["/dev/stdin"], plain)
    tmp = path + ".tmp"
    fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "wb") as f:
        f.write(out)
    os.replace(tmp, path)


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("env", choices=["local", "staging", "production"])
    ap.add_argument("--age", default="", help="recipients, comma separated, instead of .sops.yaml")
    a = ap.parse_args()
    if a.env == "local" and not a.age:
        print("rotate-secrets.py: the local environment has no .sops.yaml rule; pass --age", file=sys.stderr)
        return 2
    root = os.path.abspath(os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", ".."))
    current = os.path.join(root, "infra/secrets", a.env, "secrets.enc.yaml")
    previous = os.path.join(root, "infra/secrets", a.env, "secrets.previous.enc.yaml")
    if os.path.exists(previous):
        print(f"rotate-secrets.py: {previous} exists: finish or remove the earlier rotation first", file=sys.stderr)
        return 1
    old = yaml.safe_load(sops(["decrypt", "--input-type", "yaml", "--output-type", "yaml", current]))
    if old.get("sa_secrets_environment") != a.env:
        print(f"rotate-secrets.py: {current} belongs to {old.get('sa_secrets_environment')}", file=sys.stderr)
        return 1

    # new-secrets.py prints a fresh document; its own input handling reads
    # the SA_* variables, so a new provider-issued value lands in it the same way
    from io import StringIO

    buf, real = StringIO(), sys.stdout
    sys.stdout = buf
    try:
        sys.argv = ["new-secrets.py", a.env]
        rc = new_secrets.main()
    finally:
        sys.stdout = real
    if rc != 0:
        return rc
    fresh = yaml.safe_load(buf.getvalue())

    new = dict(fresh)
    for k in KEPT:
        if k in old:
            new[k] = old[k]
    for key in new_secrets.INPUTS.values():
        if key not in fresh and key in old:
            new[key] = old[key]
            print(f"rotate-secrets.py: {key} kept (no new value given); rotate-all.yml will list it", file=sys.stderr)
    # keys only an operator adds (the probe's token and key) are kept; they
    # are re-issued through the console, not generated
    for k, v in old.items():
        if k not in new:
            new[k] = v
            print(f"rotate-secrets.py: {k} kept (not generated)", file=sys.stderr)

    # the certificate files first: when one cannot be read with this
    # identity, nothing has been rewritten yet
    rekeyed = []
    for name in ("ca.enc.yaml", "tls.enc.yaml"):
        path = os.path.join(root, "infra/secrets", a.env, name)
        if os.path.exists(path):
            doc = yaml.safe_load(sops(["decrypt", "--input-type", "yaml", "--output-type", "yaml", path]))
            encrypt(doc, path, root, a.age)
            rekeyed.append(name)
    encrypt(old, previous, root, a.age)
    encrypt(new, current, root, a.age)
    changed = sorted(k for k in new if new.get(k) != old.get(k))
    print(f"rotated {len(changed)} keys of {a.env}; previous values in {previous}")
    if rekeyed:
        print(f"re-encrypted {', '.join(rekeyed)} of {a.env} to the new recipients, values unchanged")
    return 0


if __name__ == "__main__":
    sys.exit(main())
