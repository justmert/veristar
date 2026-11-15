---
title: "Supply chain"
description: "How images and the tracker are built, gated, signed, verified and kept up to date."
---

This page describes how the platform's images and the tracker are built, gated, signed, verified
and kept up to date. Production runs release `v0.2.1`, whose images are signed with cosign
(section 2.1) and verified before any node pulls them (section 3).

## 1. What is protected

| Artifact | Built by | Gated by | Signed or attested | Verified by |
|---|---|---|---|---|
| Platform images (gateway, collector, caddy, console, controlplane, replayer, metricswriter, jobs, platformctl, prober, postgres-ha) | `.github/workflows/release-images.yml` on a tag `v*`, or `scripts/supply-chain/release-workstation.sh` (section 2.1) | syft SBOM, Trivy (fixable critical or high fails) | cosign signature and SBOM attestation with the release key (key mode, production); or a keyless signature with build provenance and SBOM attestations (keyless mode, section 3) | `infra/ansible/playbooks/verify-release.yml`, imported first by `site.yml`, `deploy.yml`, `rotate-all.yml` and `restore.yml`, before any node pulls |
| `sdk.js` (tracker) | `packages/tracker/build.mjs`, committed under `releases/<version>/` | immutability, byte-identical rebuild, size (`verify-releases`) | build provenance attestation by `release-tracker.yml` | tenants: the SRI hash in the snippet and a byte-identical rebuild ([verify-tracker.md](verify-tracker.md)) |
| npm package of the tracker | `packages/tracker/scripts/pack.mjs` in `release-tracker.yml` on a tag `tracker-v*` | packs to the same bytes twice, installs and type-checks in a consumer | npm provenance (trusted publishing) and a build provenance attestation | `npm audit signatures` |
| Upstream images (Umami, NATS, etcd, HAProxy, the observability images) | upstream | pinned by tag and index digest in `infra/ansible/vars/pins.yml` and the observability role defaults; the deploy refuses an upstream reference without a digest | upstream | digest pin |

Tool versions: cosign 3.1.4, syft 1.57.1, Trivy 0.76.2, gitleaks 8.30.1,
govulncheck 1.9.0. Workflows install them with `scripts/supply-chain/install-tools.sh`, which checks
each download against the release's own checksum file. Every GitHub Action is pinned by commit SHA
(resolved with `git ls-remote`, tag in a comment).

## 2. Image release (`release-images.yml`)

For each image, in `.github/actions/release-image/action.yml`:

1. Build with BuildKit and push to `ghcr.io/<owner>/<repo>/<name>:<tag>`. The postgres-ha tag is
   `<postgresql version>-<tag>`, because the core role compares the version.
2. `scripts/supply-chain/scan-image.sh <repo>@<digest>`: an SPDX SBOM with syft, the full Trivy
   report as JSON, then the gate: Trivy with `--severity CRITICAL,HIGH --ignore-unfixed
   --exit-code 1`. Unfixed findings are recorded but do not fail the release, because only an
   upstream fix or a mitigation can address them ([../runbooks/security-patch.md](../runbooks/security-patch.md)).
3. Only an image that passed the gate is signed, in the signing mode of the repository variable
   `SIGNING_MODE` (`key` when unset; section 3):
   - key mode: `cosign sign --key env://COSIGN_PRIVATE_KEY --tlog-upload=false`, and
     `cosign attest --type spdxjson` for the SBOM, with the release key;
   - keyless mode: `cosign sign --yes <repo>@<digest>` with a short-lived Fulcio certificate issued
     to the workflow's GitHub OIDC identity and recorded in the Rekor transparency log, then
     `actions/attest` v4.2.2 writes a build provenance attestation and an SBOM attestation.

An image that failed the gate stays in the registry unsigned. The deploy refuses unsigned digests,
so such an image cannot be deployed.

The last job writes `release-images.json` (schema 1: release tag, and per image the repository,
tag and digest; `scripts/supply-chain/release-manifest.py` refuses a set of names other than the
expected eleven), verifies every digest again the way the deploy does, and attaches the file to
the GitHub release (in keyless mode it also attests the file).

### 2.1 Release from a workstation

A release can also be built and signed on a workstation, with the same gate and the same key as
the workflow's key mode (section 3). `scripts/supply-chain/release-workstation.sh <tag> <work dir>`
runs every step below; it refuses a checkout that is not exactly at the tag or that has local
changes. Release `v0.1.0` was built by `release-images.yml`; `v0.2.0`, `v0.2.1` and `v0.2.2` were built
on a workstation; `v0.2.3` was built by `release-images.yml` in keyless mode. The steps, run from a clean checkout of the tag:

1. **Build and push** each image of the matrix in `release-images.yml` with the same Dockerfile,
   context and build arguments (`VERSION=<tag>`, `DBIP_MONTH` from the tag's commit month,
   `CMD=` for the Go services, `CADDY_IMAGE=<caddy repo>@<digest>` for the console, the
   `<PG_VERSION>-<tag>` tag for `postgres-ha`): `docker buildx build --push --provenance=false
   --sbom=false -t ghcr.io/justmert/veristar/<name>:<tag> ...`, signed in to `ghcr.io` with a
   token of the release account that has `write:packages` (`GH_TOKEN`), kept out of the
   repository.
2. **Gate** each pushed digest with `scripts/supply-chain/scan-image.sh <repo>@<digest> <dir>`
   (syft SBOM, Trivy fails on a fixable critical or high finding); stop at the first failure.
3. **Sign and attest** with the SOPS copy of the release key, never written to disk in plain
   text:
   `sops exec-env infra/secrets/production/secrets.enc.yaml 'COSIGN_PRIVATE_KEY="$cosign_private_key" COSIGN_PASSWORD="$cosign_password" cosign sign --yes --key env://COSIGN_PRIVATE_KEY --use-signing-config=false --tlog-upload=false <repo>@<digest>'`,
   and `cosign attest` with `--type spdxjson --predicate <sbom>` the same way.
4. **Write the manifest** with `scripts/supply-chain/release-manifest.py <digest dir> <tag>`
   (one `{"name","repo","tag","digest"}` file per image, as the workflow's artifacts), verify
   every digest with `cosign verify --key infra/ansible/vars/release-cosign.pub
   --insecure-ignore-tlog=true`, and attach `release-images.json` to the GitHub release
   (`gh release create <tag> release-images.json`).

The deploy verifies a workstation release like a key-mode workflow release: the key, the
manifest and the registry digests are the same. Production verifies in keyless mode since
`v0.2.3`, so deploy a workstation release with `ANSIBLE_EXTRA='-e sa_signature_mode=key'`.

**Release `v0.2.0` (2026-11-14).** Built by
`scripts/supply-chain/release-workstation.sh v0.2.0 <work dir>` from a clean worktree at the tag
(commit `e166062` on `main`, with the MCP server, migration 00030, the Umami theme, the event
route without an access log, and `GOGC=200` with adaptive goroutine stacks for Caddy and the
gateway). The
script ran every step above in 9 min 36 s: eleven images built and pushed, each gated by `scan-image.sh` (syft 1.57.1,
Trivy 0.76.2; no image had a fixable critical or high finding), signed and SBOM-attested with the
release key read from the SOPS file into the process's environment only (`env://`), no
transparency log; then `release-images.json` written, every digest verified with
`infra/ansible/vars/release-cosign.pub`, and the file attached to the GitHub release `v0.2.0`.

| Image | Tag | Digest |
|---|---|---|
| `caddy` | `v0.2.0` | `sha256:5318e90aad4e2cbc0fc7a37c34e11cfc48ae7cb5d4caa44a4ae276ce5e733c87` |
| `collector` | `v0.2.0` | `sha256:dc9d7a2fe726c281766eff1b2cb81ac70b650177b3206fa59e8f73c3d7af3157` |
| `console` | `v0.2.0` | `sha256:f6da6cd2462d6eae94c49fdb96935527098fd2ecea93c7d3a2759503b31d2b7b` |
| `controlplane` | `v0.2.0` | `sha256:d30e7f1aad5a1cd28801019c02b8afeba7cfd83de153a2abeddaf2326a7e4432` |
| `gateway` | `v0.2.0` | `sha256:f42f571d7c2ca008d1ac23c88a4fe19853de2fcf3a392d4ef678a6944fdcd32f` |
| `jobs` | `v0.2.0` | `sha256:9591fb62d92c22920605fab247832ae1611605d1df0ebd9f3fb84a39aae7babf` |
| `metricswriter` | `v0.2.0` | `sha256:44952f03a3a3aff106e505614daca2998f970c8dc61bb9fad3f63144905d33c4` |
| `platformctl` | `v0.2.0` | `sha256:517c687501cddc4161fa3cb51061fa412a75d1601f244c7370d613a203418d38` |
| `postgres-ha` | `18.7-v0.2.0` | `sha256:ac80c5ba3ca0ab798bfc5d40a00da5cb84fa09fdf0457d06d5f9ed158cddd6e5` |
| `prober` | `v0.2.0` | `sha256:b5e3f5a5e99945dc954a04d6e21696517c7b8a1f9635893c1ec6984ac12b79f8` |
| `replayer` | `v0.2.0` | `sha256:b89d0ef67613e0dc8914160a5e3b4d13a28678de4aa75fe6af9f87432d1a8a9a` |

**Release `v0.2.1` (2026-11-14).** Built the same way by
`scripts/supply-chain/release-workstation.sh v0.2.1 <work dir>` from a clean worktree at the tag (commit `d9478b9` on `main`. Since `v0.2.0` it adds `platformctl probe-tenant
rotate-otlp-token`, the HAProxy start fix, the replica's WAL fetch from the pgBackRest repository,
and the probe scrape and watchdog wiring) in
8 min 44 s: eleven images gated by `scan-image.sh` (no fixable critical or high finding), signed
and SBOM-attested with the release key from the SOPS file, `release-images.json` verified with
`infra/ansible/vars/release-cosign.pub` and attached to the GitHub release `v0.2.1`. The builds
are reproducible: `caddy`, `collector`, `console` and `postgres-ha` have no source change since
`v0.2.0` and came out with the same digests. `deploy.yml` verified every digest with the release key
before it touched a node.

| Image | Tag | Digest |
|---|---|---|
| `caddy` | `v0.2.1` | `sha256:5318e90aad4e2cbc0fc7a37c34e11cfc48ae7cb5d4caa44a4ae276ce5e733c87` (same as `v0.2.0`) |
| `collector` | `v0.2.1` | `sha256:dc9d7a2fe726c281766eff1b2cb81ac70b650177b3206fa59e8f73c3d7af3157` (same as `v0.2.0`) |
| `console` | `v0.2.1` | `sha256:f6da6cd2462d6eae94c49fdb96935527098fd2ecea93c7d3a2759503b31d2b7b` (same as `v0.2.0`) |
| `controlplane` | `v0.2.1` | `sha256:22b9b90af6aa1ecdd195cff099b1dbffda001b84a30242cac54060cb0fcc071c` |
| `gateway` | `v0.2.1` | `sha256:1aef0aab6423d6c8190d47d13c776351630a1e62a87eec7fe5aa7252ee9652b5` |
| `jobs` | `v0.2.1` | `sha256:bb29d16b9077ce34e6ba89c5f222e28565cb6f25273c6d9d57261d6c082b073e` |
| `metricswriter` | `v0.2.1` | `sha256:e3342f9e559b4ad6d6fba86526cf94c3d79bf85fcb8211eb0a83c4bdd3d1d469` |
| `platformctl` | `v0.2.1` | `sha256:84f5069367fb35f9dbebe149fec0c9c97b3eaa4382b93e2f394d8c16d943e322` |
| `postgres-ha` | `18.7-v0.2.1` | `sha256:ac80c5ba3ca0ab798bfc5d40a00da5cb84fa09fdf0457d06d5f9ed158cddd6e5` (same as `v0.2.0`) |
| `prober` | `v0.2.1` | `sha256:63c87826ade03405133351c2487806d6c086c6a02c5659cce620aac20917d9b3` |
| `replayer` | `v0.2.1` | `sha256:6b996c7f140c7b677b0ee29b08f5f948f81874bdbbb4ac16210a752224af66e6` |

**Release `v0.2.2` (2026-11-14).** Built the same way by
`scripts/supply-chain/release-workstation.sh v0.2.2 <work dir>` from a clean worktree at the tag
(commit `e57b724` on `main`. Since `v0.2.1` it adds the console redesign with the light theme,
account deletion with `DELETE /api/me` and migration 00031, the Umami theme's light mode, and the
prober's HTTP/2 health check) in 9 min 20 s: eleven images gated by `scan-image.sh` (no fixable
critical or high finding), signed and SBOM-attested with the release key from the SOPS file,
`release-images.json` verified with `infra/ansible/vars/release-cosign.pub` and attached to the
GitHub release `v0.2.2`. `caddy` and `postgres-ha` have no source change since `v0.2.0` and came
out with the same digests. `deploy.yml` verified every digest with the release key before it
touched a node.

| Image | Tag | Digest |
|---|---|---|
| `caddy` | `v0.2.2` | `sha256:5318e90aad4e2cbc0fc7a37c34e11cfc48ae7cb5d4caa44a4ae276ce5e733c87` (same as `v0.2.0`) |
| `collector` | `v0.2.2` | `sha256:80fe4daf9464e1131eb5e7764f88e62fb3e43b85ac27e21f1d7962384f70946e` |
| `console` | `v0.2.2` | `sha256:79634a938c2283f898688cb0b85fbcc8576932d886cd150cd6cce445d1256ffc` |
| `controlplane` | `v0.2.2` | `sha256:1f264700b93256d412a5d15b91ffd393eb737fe9589fbd9ae0d8b9804a7a2335` |
| `gateway` | `v0.2.2` | `sha256:c85c3301817b62b9c8760a4ff215548323de022aff1fedc6cf935eca3c37cba8` |
| `jobs` | `v0.2.2` | `sha256:de58464064aa111c805a65fae9e7b512603c3d78071d89dae171c0561aa4b7ae` |
| `metricswriter` | `v0.2.2` | `sha256:1043fdf46c083931cde90357bddb28d26a256fe1837c06cef010fba98f1312b2` |
| `platformctl` | `v0.2.2` | `sha256:c124f9d86286d5729f2da8dcd14eefa228e45041ad24874d3219e8f2211b9f9d` |
| `postgres-ha` | `18.7-v0.2.2` | `sha256:ac80c5ba3ca0ab798bfc5d40a00da5cb84fa09fdf0457d06d5f9ed158cddd6e5` (same as `v0.2.0`) |
| `prober` | `v0.2.2` | `sha256:b80c2df685a5edbe6d82a64d141b91349b4c2799c52a31385be5f994d624d5dd` |
| `replayer` | `v0.2.2` | `sha256:75bf539d3b794ab7b29db45008efe24ee0377897f03d63ef4f37aa73d3af2458` |

**Release `v0.2.3` (2026-11-15), the release production runs.** The first release after the
repository became public, built by `release-images.yml` on the tag push (tag `v0.2.3` at commit
`5be16fb` on `main`) in keyless mode, from 02:14:41 to 02:20:43 UTC: eleven images gated by
`scan-image.sh` (no fixable critical or high finding), signed with the workflow's Fulcio
certificate and logged in Rekor, with build provenance and SBOM attestations; the manifest job
verified every digest with the identity
`https://github.com/justmert/veristar/.github/workflows/release-images.yml@refs/tags/v0.2.3` and
attached `release-images.json`. The packages are public: every digest verified from a docker
configuration without credentials. Since `v0.2.2` it adds the anonymous image pulls with the
registry sign-out of every node, keyless verification in `vars/signing.yml`, and the npm advisory
fixes in test and example dependencies, which leave the console, website and tracker bundles
byte-identical.

| Image | Tag | Digest |
|---|---|---|
| `caddy` | `v0.2.3` | `sha256:36d14aa5273787ce7ac503fec870a6ed7a1f8b9c4f3e35a0d8effac5b3427ea1` |
| `collector` | `v0.2.3` | `sha256:e0e4e7ae34b0d7515ad1f659d4c445d811463d7ae7d5f0bacd44738b5282a6ce` |
| `console` | `v0.2.3` | `sha256:2a8fc19953f7ce14bfa7a55f1378a78f26f6988721c72c1ed275924a26ebdd14` |
| `controlplane` | `v0.2.3` | `sha256:44c0a3c40937e73a127c6d53bb0dbc147a137804bea924cb51c826a69eb0dfd3` |
| `gateway` | `v0.2.3` | `sha256:f05994378e8361135244180bf754dc04dee77fa30e170aa95e8cff186bb3f288` |
| `jobs` | `v0.2.3` | `sha256:732fe21fe7efcdf1fe3473533568e01e009389ecfdbc5303ae38fa521ccb8e2b` |
| `metricswriter` | `v0.2.3` | `sha256:12e4de0e5c4e839ea1b0d075406dedce8d17afa84be6716875a4cf3f46de5a08` |
| `platformctl` | `v0.2.3` | `sha256:a9db7c36747bb53ffcc991a964a59ba5701fc09a4aa13f1b5560a215bcd96587` |
| `postgres-ha` | `18.7-v0.2.3` | `sha256:d07c1cbaf0709571bf4d185d2f43ed0c9f4629da8e4de4ccef511d815f3ac84a` |
| `prober` | `v0.2.3` | `sha256:8712c6a2c1da43b52a797824719b3a54fea151adeb61703671abf4313c151ae8` |
| `replayer` | `v0.2.3` | `sha256:f2830f57443201b2543a03207ee2ddec08183f366b16bb6c28fb0b416f8cdac4` |

## 3. Deploy-time verification (every playbook that pulls images)

`site.yml` (first converge and node replacement), `deploy.yml` (rolling deploy), `rotate-all.yml`
and `restore.yml` all start with `playbooks/verify-release.yml`, whose first play runs
`infra/ansible/playbooks/tasks/verify-images.yml` on the controller. The local converge is the one exception, refused for any other environment: its images come unsigned from
its throwaway registry, and the play says so instead of verifying.

```
ansible-playbook -i inventories/<env>/ playbooks/deploy.yml \
  -e sa_image_tag=<release> -e sa_release_manifest=<path to release-images.json>
```

The run stops before any node is touched when:

- no manifest is given, or it names another release, another registry, or not every image in
  `platform_images`;
- an upstream image in `vars/pins.yml` is not pinned by digest;
- `cosign verify` fails for any digest in the signature mode of `vars/signing.yml`:
  - `key` (releases `v0.1.0` to `v0.2.2`): the committed public key
    `infra/ansible/vars/release-cosign.pub`, with `--insecure-ignore-tlog`. This covers an unsigned
    digest and a signature by any other key;
  - `keyless` (the production mode since `v0.2.3`): identity
    `https://github.com/justmert/veristar/.github/workflows/release-images.yml@refs/tags/<release>`
    and issuer `https://token.actions.githubusercontent.com`. This covers an unsigned digest and a
    signature from any other workflow, branch, tag or repository;
- the release tag in the registry now points to a digest other than the manifest's.

### Signing modes

`infra/ansible/vars/signing.yml` sets `sa_signature_mode: keyless`, and `release-images.yml` signs in
the mode of the repository variable `SIGNING_MODE` (`key` when unset; set to `keyless` since release
`v0.2.3`). The two modes:

| Step | Key mode | Keyless mode |
|---|---|---|
| Build, push to `ghcr.io/justmert/veristar/*` with `GITHUB_TOKEN` | yes | yes |
| SBOM (syft) and Trivy gate | yes | yes |
| Signature | `cosign sign --key env://COSIGN_PRIVATE_KEY --tlog-upload=false` (Actions secrets `COSIGN_PRIVATE_KEY`, `COSIGN_PASSWORD`) | Fulcio certificate for the workflow, logged in Rekor |
| SBOM attestation | `cosign attest --key ... --tlog-upload=false --type spdxjson` | `actions/attest` |
| Build provenance attestation | not produced | `actions/attest` |
| Manifest check before publishing | `cosign verify --key infra/ansible/vars/release-cosign.pub --insecure-ignore-tlog` | identity and issuer as above |

In key mode the deploy trusts exactly one key, the committed public half
`infra/ansible/vars/release-cosign.pub`. Signatures are not written to a transparency log, and
no build provenance attestation is produced. The private key is held in the Actions secrets
`COSIGN_PRIVATE_KEY` and `COSIGN_PASSWORD` and in the production SOPS file
(`cosign_private_key`, `cosign_password`); rotation is in
[../runbooks/secret-rotation.md](../runbooks/secret-rotation.md).

The production packages are public, so nodes and the controller pull without signing in
(`sa_registry_login: ""`). When the registry packages need a sign-in, every node signs in to `ghcr.io` with a classic token
that has `read:packages` only (`ghcr_pull_token`, `sa_registry_login` in the inventory,
`roles/secrets/tasks/registry.yml`, credential at `/root/.docker/config.json`, mode 0600). The
controller gives cosign the same token through a docker config made for that run only
(`playbooks/tasks/registry-auth.yml`).

**Switching to keyless mode.** GitHub artifact attestations need a public repository (or GitHub
Enterprise Cloud), and a keyless signature writes the image name and digest to the public Rekor
log. To switch, set the repository variable `SIGNING_MODE=keyless` and
`sa_signature_mode: keyless` in `infra/ansible/vars/signing.yml`, cut a new release and deploy it.
With public packages, then set `sa_registry_login: ""`, deploy (the `secrets` role signs every node
out of the registry), and delete the pull token. Release `v0.2.3` made both switches.

Release `v0.1.0` (2026-11-13) was the first release in key mode: all eleven images were built,
passed the gate and were signed and attested with the release key, and the manifest job verified
each digest with the public key before it attached `release-images.json`.

The next play sets `platform_images` on every node to the verified `repo:tag@digest` references.
Host facts outrank play variables, so the roles (including the imported bootstrap play) pull exactly
the verified digests. The roles themselves are unchanged.

**Local proof (test-only key).** Keyless signing needs GitHub's OIDC token, which exists only inside
GitHub Actions. `tests/supplychain` (`make supplychain-it`) proves the refusal logic with a real
registry and real cosign signatures made with an **ephemeral key pair created for one test run**
(`sa_signature_mode: test-ephemeral-key`). The task file refuses that mode for any inventory outside
`tests/`, and a test case proves the refusal. This is a test of the verification logic, not the
production signing design. Result on 2026-11-13: 10 of 10 cases pass (signed release passes and pins
digests; unsigned digest, foreign key, other build under the release tag, moved tag, other release,
other registry, incomplete manifest, missing manifest, test mode outside tests: all refused).

**Probe images.** The probe node's own images (the `prober` and the custom Caddy, from the observability role) are verified the
same way: the manifest must name the `prober` (it is in `platform_images`), the pin play sets
`platform_images` on the probe too, and the observability role names its `prober` and `caddy`
through `platform_images`. The role also refuses a probe node whose `prober` or `caddy` is not
pinned by digest outside the local converge, so a run that skipped the check stops. The probe
bundle of [../deploy/probe.md](../deploy/probe.md) uses the same check: `playbooks/probe-bundle.yml` imports
`verify-release.yml` and refuses a prober not pinned by digest.

## 4. Tracker release (`release-tracker.yml`)

A tag `tracker-v<x.y.z>` must equal `packages/tracker/package.json` and the newest entry of
`releases/manifest.json`. The `prove` job runs `verify-releases` (committed files unchanged,
consistent manifest, byte-identical rebuild of `sdk.js` on the runner, size limit) and the tracker
tests, including `test/pack.test.ts`: the npm package packs to the same tarball twice, installs into
an empty consumer, works with no `__SDK_VERSION__` define (the published ESM carries the version),
type-checks, and carries `sdk.js` byte for byte. The `publish` job attests `sdk.js` and the tarball,
then runs `npm publish` with trusted publishing: npm exchanges the job's OIDC token for a
short-lived publish credential and attaches provenance itself. It then creates the GitHub release
with `sdk.js`, the manifest and the tarball. The package name is the repository variable
`NPM_PACKAGE`, a scoped name whose trusted publisher on npm is this repository and workflow file;
the publish job stops when the variable is not set.

## 5. Detection (`security.yml`, hourly)

- `scripts/supply-chain/advisories.py`: every published repository advisory of
  `umami-software/umami` not listed in `docs/security/advisories-reviewed.json`, any newer Umami
  release, GitHub advisory database matches for Next.js, React and React DOM at the exact versions
  in the pinned Umami's `pnpm-lock.yaml`, for nats-server, Caddy, etcd and the collector modules at
  the pinned versions, and a newer PostgreSQL minor of the pinned major. It never reads "fixed in" wording,
  because Umami has published advisories without a fixed version. A source that cannot be read
  fails the job; it is never treated as clean.
- Trivy on the digests a deploy uses: the platform images of the newest `release-images.json` and
  every upstream digest pin.
- `govulncheck` on both Go modules, `pnpm audit` on the workspace.
- `scripts/supply-chain/report-findings.py` files what is left in issues labelled
  `security-finding`:
  - Image findings that `docs/security/advisories-reviewed.json` lists under
    `upstream_image_findings` for the image's exact tag carry a recorded decision (section 8.1)
    and are not filed.
  - The remaining findings of one image share one issue, edited in place every hour. A finding
    that several binaries of the image report (Grafana's bundled plugins) appears once. When an
    image the run scanned has nothing undecided left, its issue is closed with a comment; when
    the image scan job fails, no issue is closed.
  - Every other finding (an advisory, govulncheck, `pnpm audit`) gets its own issue, keyed by its id.
- The run posts one alert to `ALERT_WEBHOOK_URL`, the optional Discord webhook, naming only the
  findings no issue listed before. Without the webhook the run ends red after filing the issues, so
  GitHub's own failure notice reaches the maintainers.

Dependabot (`.github/dependabot.yml`) opens update pull requests for Go, npm, Dockerfiles,
compose files, GitHub Actions, OpenTofu providers, the Python and Rust examples and the Ansible
controller requirements. Umami is excluded: its digest moves only through `make umami-upgrade`.
The digests in `infra/ansible/vars/pins.yml` are not a format Dependabot reads; the hourly Trivy
run covers them.

## 6. Pull request gates (`ci.yml`)

`gitleaks` (whole history, reviewed fingerprints in `.gitleaksignore`), `licences`
(`scripts/supply-chain/licences.py`, see section 7), `isolation` (`make isolation`) and
`supplychain` (`make supplychain-it`), next to the existing build, lint, test and tracker checks.
[branch-protection.md](branch-protection.md) lists every check and when it runs.

## 7. Licences

**Policy.** Everything this project ships, and every production dependency of it, carries a licence
on the OSI approved list. There are no exceptions for shipped code. Dependencies that are never
shipped (development tools, test fixtures, example apps and operator tooling) are listed in a
separate section of the report. Any of them under a licence that is not OSI approved must be on an
explicit allowlist, reviewed for one exact version and licence, with a written reason. Nothing is
skipped silently: a package that cannot be placed in either section, or whose licence cannot be
found, fails the gate.

`scripts/supply-chain/licence-gate.sh` (the `licences` job in `ci.yml`, and local runs) feeds
`scripts/supply-chain/licences.py`, which reads `docs/security/licence-policy.json`:

| Section | What it contains | How it is derived | Fails on |
|---|---|---|---|
| shipped | Go modules linked into a platform binary; npm packages of the console bundle, `sdk.js` and the npm package of the tracker | `go list -deps ./cmd/...` in the root module and `go list -deps .` in the collector distribution that ocb generates (`dist/scf-otelcol`, from `collector/builder-config.yaml`); the `dependencies` and `optionalDependencies` closure of the shipped importers `apps/console` and `packages/tracker` in `pnpm-lock.yaml` | any licence not on the OSI list |
| not shipped | everything else in `go.mod`, the collector module, `pnpm-lock.yaml`, the example lockfiles (`package-lock.json`, `uv.lock`, `Cargo.lock`, `go.mod`) and `infra/ansible/requirements.txt`, each with the importers that pull it in | the rest of the same graphs | a non-OSI licence without a matching allowlist entry, and any allowlist entry that no longer matches (package gone, upgraded, relicensed or now shipped) |

Three checks keep the shipped section honest instead of trusting `package.json`:

- The console bundle is built with its own Vite configuration and every npm package whose code lands
  in it is listed (`apps/console/scripts/bundle-packages.mjs`). Each must be in the shipped section,
  so a package imported at runtime but declared as a devDependency fails the gate. CSS is compiled
  by the Tailwind plugin and is not part of that module graph, so the policy names what
  `src/index.css` imports: `tailwindcss` (a devDependency) counts as shipped, and of `shadcn` only
  the file `shadcn/tailwind.css` is used, so `shadcn` is shipped but the dependencies of its CLI
  are not.
- The tracker builds (`sdk.js` in `build.mjs`, the npm package in `scripts/pack.mjs`) fail when
  esbuild reports any input outside `src/` and `releases/`, and the packed `package.json` declares
  no dependencies. The tracker therefore ships no third-party code, including no Wallets Kit code.
- Go: only modules that are linked into a shipped binary count as shipped. Test-only modules such
  as testcontainers are listed as not shipped.

Packages whose licence trivy did not find on disk are looked up in the npm, PyPI or crates.io
registry, and Go modules in deps.dev, which reads the licence from the module zip. Trivy 0.76.2 does
not classify the MIT-0 text of `github.com/segmentio/asm`, for example. The project's own modules (the root module, the collector distribution and the example roots) are
reported separately as own code under the repository's Apache-2.0 licence.

**Not covered by this gate.** The Caddy binary (built by xcaddy inside
`deploy/images/caddy.Dockerfile` with two plugins) and the operating system packages of the base
images (distroless, Alpine in the Caddy image, Debian in `postgres-ha`) are not part of these
graphs. The image SBOMs from `scan-image.sh` list them.

**Why the Wallets Kit packages are allowlisted.** `@creit.tech/stellar-wallets-kit` 2.7.1 and 2.8.0
are installed only as devDependencies of `packages/tracker` (`swk-2.7.1`, `swk-2.8.0`, so the
wallets kit adapter tests run the real kit) and as a dependency of the example app
`examples/wallets-kit-spa`, a sample for tenants that is also a test target and is never deployed.
The lockfile graph shows no other path to them. Checked on 2026-11-13: esbuild's input list for
`sdk.js` and for the npm entry points contains only `src/` and `releases/` files; the packed npm
package (`@sa-local-test/tracker` 1.1.0) has no dependency fields, no bare imports and no kit
code; the console bundle contains none of the kit's packages and no `walletconnect`, `reown`,
`creit.tech`, `xbull` or `trezor` string; the SBOMs of all 12 platform images list none of them. The
kit pulls in 25 lockfile entries the gate reports as non-OSI (24 distinct versions): 19 Reown and
WalletConnect versions and `@trezor/crypto-utils` under "SEE LICENSE IN LICENSE.md" (not an OSI
licence), and four with no licence in their registry metadata (`@creit.tech/xbull-wallet-connect`,
`@hot-wallet/sdk`, `@near-wallet-selector/core`, `text-encoding-utf-8`). The project
does not bundle or redistribute any of them, so they are allowlisted as test and example
dependencies. Tenants who add the kit to their own sites take it from npm under its own terms.

The other allowlisted packages are data, not code: `caniuse-lite` (CC-BY-4.0, browser support data
for build tools; it reaches the console importer only through the shadcn CLI and is not bundled),
`mdn-data` (CC0-1.0, CSS reference data for test and example tooling) and `webpki-root-certs`
(CDLA-Permissive-2.0, root certificates in the Rust example app). All three are accepted only while
they are not shipped.

**Changing the allowlist.** Add or update an entry in `not_shipped_allowlist` with the exact
version and the licence string the report printed, a reason that says why the package is not
shipped and what it is, and the review date. The reviewer checks the report's "pulled in by" line.
A version bump of an allowlisted package fails the gate until the entry is reviewed again.

## 8. Results recorded on 2026-11-13

- Trivy and syft on all 12 platform images built from this tree and the 11 upstream images. All 12
  platform images pass the gate after three fixes: `github.com/klauspost/compress` 1.20.1 to 1.20.4
  (GO-2026-6865, called from the replayer), Caddy built with `golang.org/x/net` v0.62.0
  (CVE-2026-78661 and others), and `postgres-ha` without the unused `gosu` (Go 1.24.6 stdlib,
  one critical and ten high) and with the Debian `zlib1g` security update.
- Upstream images with fixable high findings that only their maintainers can fix: Umami 3.5.0
  (`undici` 6.28.0), HAProxy 3.4.7 (`zlib1g`), etcd 3.6.16, Prometheus 3.15.1, Alertmanager 0.34.2,
  Grafana 13.2.6, Gatus 5.37.0, node_exporter 1.12.1, postgres_exporter 0.20.1,
  prometheus-nats-exporter 0.20.3. Section 8.1 records, per finding, the pin bump or the
  mitigation and its reasoning.
- govulncheck: no called vulnerability in either module after the fixes (the example app moved to
  `golang.org/x/net` v0.62.0). One uncalled module finding remains (`golang.org/x/crypto/openpgp`,
  GO-2026-5932, fixed only in a pseudo-version; the package is not imported).
- A throwaway build of `postgres-ha` with `gosu` left in fails the gate: 1 critical, 25 high,
  exit 1.

### 8.1 Upstream image findings: bump or mitigation (2026-11-13)

Every fixable critical or high finding of the upstream images, rechecked on 2026-11-13 with
Trivy 0.76.2 (`--scanners vuln`, registry images by digest, one at a time). For each image the
registry was checked for a release that fixes it (the pin policy takes a younger minor line when it
carries a security fix). Where no release fixes a Go finding, the image's binary
was checked with `govulncheck -mode=binary` (v1.9.0, database of 2026-11-11), which reports
whether the vulnerable symbols are compiled into the binary at all. No critical finding exists in
any upstream image.

**Bumped (each rescanned after the change):**

| Image | Was | Now | Findings before, after | Checks |
|---|---|---|---|---|
| HAProxy | `haproxy:3.4.7` (Debian trixie) `sha256:f6d2ee87...` | `haproxy:3.4.7-alpine` `sha256:b9fe316f718d5015c5cfebd8ca019591f3e44656f61d93cb8b59a1028961e530` | 2 high (`zlib1g` CVE-2026-76844, CVE-2026-85091; no rebuilt trixie image exists), 0 | Same HAProxy release, Alpine 3.24 base. Both builds run as uid 99 and are built with `PROMEX`; `haproxy -c` passes on a rendered core `haproxy.cfg` and on the edge and core relay configs with both builds |
| Gatus | `v5.37.0` `sha256:094eb186...` | `v5.38.0` `sha256:085ed63da963728965d00a3c78aa6167e3bf135218485b893201758e079e8415` | 12 high, 5 high (the Go standard library and gRPC findings are gone) | The 5.38 line is 16 days old, taken for the fixes. The v5.37.0...v5.38.0 diff leaves `config/key/key.go`, `config/endpoint/result.go` and the statuses API that `internal/prober` and `cmd/sloreport` depend on unchanged. A rendered probe `gatus.yml` loads the same 5 endpoints and 20 external endpoints in both versions |

Pins moved together in `infra/ansible/vars/pins.yml`, the observability role defaults,
`deploy/images/upstream/` (`tests/pins` checks they agree) and `tests/observability/compose.yml`.

**Mitigated (no fixed release exists on 2026-11-13):**

| Image | Finding | Why it is accepted, and when to revisit |
|---|---|---|
| Umami 3.5.0 (newest release; 3.5.0 is the newest tag on GHCR) | `undici` 6.28.0: CVE-2026-109383 (TLS options dropped), CVE-2026-109869, CVE-2026-19534, CVE-2026-87018 (client-side denial of service) | All four are in undici's HTTP client. Umami makes no outbound request in this deployment: `PRIVATE_MODE`, `DISABLE_TELEMETRY` and `DISABLE_UPDATES` are set, and `TestWebPath` checks no egress from boot and during a UI session (`threat-model.md`). Inbound requests are served by Next.js, not undici. Revisit at the next Umami release through `make umami-upgrade`. |
| etcd 3.6.16 (newest 3.6 release) | `golang.org/x/net` CVE-2026-78661 (HTML parser denial of service, GO-2026-6860); the symbol is in the binary | etcd's client and peer ports are published on the private address only, the firewall accepts the private network only, and the only client is Patroni. No etcd API takes HTML. Revisit at 3.6.17. |
| Prometheus 3.15.1, Alertmanager 0.34.2 | `golang.org/x/net` CVE-2026-78661 | govulncheck: module present, vulnerable symbols not in either binary. The younger lines (Prometheus 3.16.0, Alertmanager 0.35.0, scanned) carry the same finding, so a bump would not fix it. |
| node_exporter 1.12.1 (newest) | 16 high: Go 1.26.5 standard library (CVE-2026-111943, -33818, -39821, -46600, -56853, -56858, -56859, -56860, -56862, -78667, -78669, -97031), `x/net` CVE-2026-78661 and -78669, `x/crypto` CVE-2026-56854, `x/text` CVE-2026-56851 | govulncheck: the `x/net/html`, `x/crypto`, `x/text`, CVE-2026-46600 and CVE-2026-78667 symbols are not in the binary. The rest are present and are denial of service in the HTTP server, TLS and parsers, or an x509 client issue. The exporter listens on the private address only (`--web.listen-address`), the firewall drops everything else, it has no TLS configuration and makes no outbound TLS connection, and only Prometheus scrapes it. An attacker must already be on the private network. Revisit at the next node_exporter release. |
| postgres_exporter 0.20.1 (newest) | 19 high: Go 1.26.4 standard library (CVE-2026-111943, -33818, -39821, -39822, -46600, -56853, -56858, -56859, -56860, -56862, -78667, -78669, -97031), `x/net` CVE-2026-46600, -78661 and -78669, `x/crypto` CVE-2026-56854, `x/text` CVE-2026-56851 and -56852 | govulncheck: the `x/net/html`, `x/crypto`, `x/text`, CVE-2026-39821, -39822, -56859 and -78667 symbols are not in the binary. The rest are present. The exporter is on the private address of the PostgreSQL hosts only, scraped by Prometheus, and connects only to PostgreSQL through HAProxy on the private network as a role that holds `pg_monitor` and nothing else (`tasks/core.yml`). Revisit at the next release. |
| Grafana 13.2.6 (newest 13.2) | 19 high, all in bundled plugin binaries under `data/plugins-bundled` (MySQL, Tempo, InfluxDB, Loki, Stackdriver, PostgreSQL, Pyroscope, Jaeger, MSSQL, OpenTSDB, Prometheus, Zipkin): `x/net`, Go standard library, and Tempo CVE-2026-28377 and CVE-2026-21728 | The only provisioned data source is Prometheus (`files/grafana/provisioning/datasources/prometheus.yml`); the other plugins handle a query only for a data source of their type, and none exists. The Prometheus plugin's finding is the HTML parser one, reachable only through responses of the platform's own Prometheus. Grafana is published on `127.0.0.1:3000` only (operators reach it through an SSH tunnel) with sign-up and anonymous access off. The `-slim` build has no findings but also no bundled Prometheus data source, so it cannot replace it. Revisit at 13.2.7 or when the 13.3 line is 30 days old. |
| Gatus 5.38.0 | `go-jose` CVE-2026-116587 (JWK parsing), `klauspost/compress` CVE-2026-63209, `x/image` CVE-2026-46603, `x/net` CVE-2026-78661 and CVE-2026-78669 (HTTP/2 SETTINGS) | govulncheck: the `x/image` and `x/net/html` symbols are not in the binary. `go-jose` is used only by Gatus's OIDC login, which `gatus.yml` does not configure. Gatus's port is not published: only the probe node's Caddy reaches it on the compose network, and Caddy speaks HTTP/1.1 to it, so no outside client can send it HTTP/2 frames; its own checks call only the platform's hosts. Revisit at 5.39. |
| prometheus-nats-exporter 0.20.3 (newest) | Alpine `musl` CVE-2026-107891 (`regcomp`), `zlib` CVE-2026-85091 | The exporter is a statically linked Go binary (go1.27.3), so it loads neither library. Only the image's `/entrypoint.sh` runs under BusyBox `sh` (linked to musl) to `exec` the exporter, with no regular expression and no input from outside. Revisit at the next release. |

Each row is also an entry under `upstream_image_findings` in
[advisories-reviewed.json](advisories-reviewed.json), with the image tag and the findings, so the
hourly `security.yml` workflow does not file them. A fixable high finding that is not listed there
has no recorded decision: the workflow files it, and it is handled under the 24-hour rule
([../runbooks/security-patch.md](../runbooks/security-patch.md)).

**Rechecked on 2026-11-15.** The hourly workflow reported the findings of the table above, 58
distinct ones across nine images, and no other fixable critical or high finding in any upstream
image or in the platform images of the newest release. No newer release fixes any of them. The
candidates, scanned with Trivy 0.76.2:

| Candidate | Result |
|---|---|
| Grafana 13.3.1 | The same 10 findings as 13.2.6, in the same bundled plugin binaries |
| etcd 3.7.3 | The same `golang.org/x/net` CVE-2026-78661 as 3.6.16. The minor jump would need a Patroni compatibility check and fixes nothing |
| Prometheus 3.16.0, Alertmanager 0.35.0 | The same `golang.org/x/net` CVE-2026-78661 |
| Gatus, node_exporter, postgres_exporter, prometheus-nats-exporter, Umami | No release newer than the pin |

`govulncheck -mode=binary` (v1.9.0, database of 2026-11-11) on the binaries extracted from the
pinned digests gives the same answer as above: CVE-2026-78661 is a symbol finding in etcd and a
module-only finding in Prometheus, Alertmanager, node_exporter, postgres_exporter and Gatus; the
module-only findings of node_exporter and postgres_exporter are the ones the table names.

**Umami repository advisory GHSA-vhf9-rcmp-v5fc (critical, published 2026-11-13).** An SQL
injection in Umami's legacy reports API: `POST /api/reports/*` copies `filters[*].paramName` from
the body into the SQL text, and a share token from `GET /api/share/<slug>` is enough to call it.
The advisory names versions up to 3.4.0 and no patched version. The pinned 3.5.0 is not affected,
and the route is not reachable from the internet:

- 3.5.0 contains upstream commit `4fa9769519`, which derives the placeholder name from the
  validated filter key (`src/lib/params.ts`).
- `web.Caddyfile` refuses `/api/share*` and `/compat/*` for every method, and every write method
  on `/api/*` except token verification and logout.
- `TestWebCaddyLive`, subtest `reports api sql injection` (`make test-caddy-web`), proves both
  against the pinned image. Caddy refuses the share route and all ten report routes under every
  spelling Umami routes to the same handler. On the private network, the advisory's payloads run
  as plain filters: a division by zero answers 200 and a 4 second `pg_sleep` returns at once.
  The same payloads against Umami 3.4.0 answer 500 (`division by zero`) and take 4 seconds.

No upgrade and no new deny rule is needed. The decision is the advisory's entry in
`advisories-reviewed.json`.
