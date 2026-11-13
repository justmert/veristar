"""writes release-images.json from the per-image digest records of
release-images.yml (the design). deploy.yml reads this file and deploys
each platform image by digest after verifying its signature.

    python3 scripts/supply-chain/release-manifest.py <digest dir> <release tag>

the set of names must be exactly the set the deploy expects, so a release
that lost an image in the matrix fails here instead of at deploy time.
"""

import json
import pathlib
import re
import sys

# the platform images a deploy pulls (infra/ansible/vars/pins.yml
# platform_images, plus prober for the observability role)
EXPECTED = {
    "gateway", "collector", "caddy", "console", "controlplane", "replayer",
    "metricswriter", "jobs", "platformctl", "postgres-ha", "prober",
}
DIGEST = re.compile(r"^sha256:[0-9a-f]{64}$")


def main() -> int:
    if len(sys.argv) != 3:
        print(__doc__, file=sys.stderr)
        return 2
    src, tag = pathlib.Path(sys.argv[1]), sys.argv[2]
    images = {}
    for f in sorted(src.glob("*.json")):
        rec = json.loads(f.read_text())
        if not DIGEST.match(rec["digest"]):
            print(f"{f.name}: bad digest {rec['digest']!r}", file=sys.stderr)
            return 1
        images[rec["name"]] = {"repo": rec["repo"], "tag": rec["tag"], "digest": rec["digest"]}
    if set(images) != EXPECTED:
        print(f"images {sorted(images)} differ from {sorted(EXPECTED)}", file=sys.stderr)
        return 1
    json.dump({"schema": 1, "release": tag, "images": images}, sys.stdout, indent=2, sort_keys=True)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
