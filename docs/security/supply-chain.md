# Supply chain

How the platform's images and the tracker are built, gated, signed, verified and kept up to date
(the design). Status on 2026-11-13 is stated at the end of every section, because two parts of
the design cannot run yet.

## 1. What is protected

| Artifact | Built by | Gated by | Signed or attested | Verified by |
|---|---|---|---|---|
| Platform images (gateway, collector, caddy, console, controlplane, replayer, metricswriter, jobs, platformctl, prober, postgres-ha) | `.github/workflows/release-images.yml` on a tag `v*` | syft SBOM, Trivy (fixable critical or high fails) | cosign keyless signature, build provenance and SBOM attestations (`actions/attest`) | `infra/ansible/playbooks/deploy.yml` before any node pulls |
| `sdk.js` (tracker) | `packages/tracker/build.mjs`, committed under `releases/<version>/` | immutability, byte-identical rebuild, size (`verify-releases`) | build provenance attestation | tenants: SRI hash in the snippet, `gh attestation verify` ([verify-tracker.md](verify-tracker.md)) |
| npm package of the tracker | `packages/tracker/scripts/pack.mjs` in `release-tracker.yml` on a tag `tracker-v*` | packs to the same bytes twice, installs and type-checks in a consumer | npm provenance (trusted publishing) and a build provenance attestation | `npm audit signatures`, the npm package page |
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
   upstream fix or a mitigation can address them (see the runbook).
3. Only an image that passed the gate is signed: `cosign sign --yes <repo>@<digest>`. Keyless: a
   short-lived Fulcio certificate issued to this workflow's GitHub OIDC identity, recorded in the
   Rekor transparency log. No signing key exists anywhere.
4. `actions/attest` v4.2.2 writes a build provenance attestation and an SBOM attestation for the
   digest and pushes both to the registry.

An image that failed the gate stays in the registry unsigned. The deploy refuses unsigned digests,
so such an image cannot be deployed.

The last job writes `release-images.json` (schema 1: release tag, and per image the repository,
tag and digest; `scripts/supply-chain/release-manifest.py` refuses a set of names other than the
expected eleven), verifies every digest again with the deploy's identity, attests the file, and
attaches it to the GitHub release.

## 3. Deploy-time verification (`deploy.yml`)

`deploy.yml` starts with a play on the controller that runs
`infra/ansible/playbooks/tasks/verify-images.yml`:

```
ansible-playbook -i inventories/<env>/ playbooks/deploy.yml \
  -e sa_image_tag=<release> -e sa_release_manifest=<path to release-images.json>
```

The run stops before any node is touched when:

- no manifest is given, or it names another release, another registry, or not every image in
  `platform_images`;
- an upstream image in `vars/pins.yml` is not pinned by digest;
- `cosign verify` fails for any digest with identity
  `https://github.com/justmert/stellar-analytics/.github/workflows/release-images.yml@refs/tags/<release>`
  and issuer `https://token.actions.githubusercontent.com` (`vars/signing.yml`). This covers an
  unsigned digest and a signature from any other workflow, branch, tag or repository;
- the release tag in the registry now points to a digest other than the manifest's.

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

**Status.** Built and proven locally with the test-only key. Keyless signing and `cosign verify` of a
real release have not run: GitHub Actions on the account ends every workflow in `startup_failure`.
Artifact attestations for a private repository also need GitHub Enterprise Cloud, so attestations
need the repository to be public (owner's explicit confirmation, the design). Not yet covered:
the `prober` image of the observability role is in the manifest and signed, but the observability
role is not deployed through `deploy.yml`, so its pull is not yet verified.

## 4. Tracker release (`release-tracker.yml`)

A tag `tracker-v<x.y.z>` must equal `packages/tracker/package.json` and the newest entry of
`releases/manifest.json`. The `prove` job runs `verify-releases` (committed files unchanged,
consistent manifest, byte-identical rebuild of `sdk.js` on the runner, size limit) and the tracker
tests, including `test/pack.test.ts`: the npm package packs to the same tarball twice, installs into
an empty consumer, works with no `__SDK_VERSION__` define (the published ESM carries the version),
type-checks, and carries `sdk.js` byte for byte. The `publish` job attests `sdk.js` and the tarball,
then runs `npm publish` with trusted publishing: npm exchanges the job's OIDC token for a
short-lived publish credential and attaches provenance itself. It then creates the GitHub release
with `sdk.js`, the manifest and the tarball.

**Status.** Built; the package build and its tests pass locally (5 of 5 in `pack.test.ts`, run with
a test-only scope `@sa-local-test`). Blocked: an operator input (the npm scope with this repository and
workflow registered as trusted publisher; set as repository variable `NPM_PACKAGE`), GitHub
Actions on the account, and a public repository (npm provenance and trusted publishing require it).
The control plane still refuses `TRACKER_NPM_PACKAGE` until the package is published.

## 5. Detection (`security.yml`, hourly)

- `scripts/supply-chain/advisories.py`: every published repository advisory of
  `umami-software/umami` not listed in `docs/security/advisories-reviewed.json`, any newer Umami
  release, GitHub advisory database matches for Next.js, React and React DOM at the exact versions
  in the pinned Umami's `pnpm-lock.yaml`, for nats-server, Caddy, etcd and the collector modules at
  our pins, and a newer PostgreSQL minor of the pinned major. It never reads "fixed in" wording,
  because Umami has published advisories without a fixed version. A source that cannot be read
  fails the job; it is never treated as clean.
- Trivy on the digests a deploy uses: the platform images of the newest `release-images.json` and
  every upstream digest pin.
- `govulncheck` on both Go modules, `pnpm audit` on the workspace.
- Every finding opens one issue labelled `security-finding` (an open issue for the same id is not
  repeated) and the run posts one alert to `ALERT_WEBHOOK_URL` an operator input. Without I8 the run ends
  red after filing the issues, so GitHub's own failure notice still reaches the maintainers.

Dependabot (`.github/dependabot.yml`) opens update pull requests for Go, npm, Dockerfiles,
compose files, GitHub Actions, OpenTofu providers, the Python and Rust examples and the Ansible
controller requirements. Umami is excluded: its digest moves only through `make umami-upgrade`.
The digests in `infra/ansible/vars/pins.yml` are not a format Dependabot reads; the hourly Trivy
run covers them.

## 6. Pull request gates (`ci.yml`)

`gitleaks` (whole history, reviewed fingerprints in `.gitleaksignore`), `licences`
(`scripts/supply-chain/licences.py`, see section 7), `isolation` (`make isolation`) and
`supplychain` (`make supplychain-it`), next to the existing build, lint, test and tracker checks.
[branch-protection.md](branch-protection.md) lists the required checks.

## 7. Licences

`scripts/supply-chain/licences.py` reads Trivy's package list of `go.mod`, `pnpm-lock.yaml`, the
example lockfiles, `uv.lock` and `Cargo.lock`, looks up in the npm, PyPI or crates.io registry any
package whose licence was not found on disk, and fails on any licence expression that is not on the
OSI approved list, unless a reviewed exception in `docs/security/licence-exceptions.json` names
that licence for that package (data licences only: CC-BY-4.0 for caniuse-lite, CC0-1.0 for
mdn-data, CDLA-Permissive-2.0 for webpki-root-certs).

**Result on 2026-11-13: not clean.** 1,689 packages, 26 failing. All 26 are transitive
dependencies of `@creit.tech/stellar-wallets-kit` 2.7.1 and 2.8.0, which are only development
dependencies of `packages/tracker` (the this stage adapter tests run the real kit) and a dependency of
the example app `examples/wallets-kit-spa`: 20 `@reown/*` and `@walletconnect/*` packages under
"SEE LICENSE IN LICENSE.md" (not an OSI licence), and `@creit.tech/xbull-wallet-connect`,
`@hot-wallet/sdk`, `@near-wallet-selector/core`, `@trezor/crypto-utils`, `eyes` and
`text-encoding-utf-8` with no licence in their registry metadata. None of them is in any shipped
artifact: the tracker imports only the kit's types, and neither `sdk.js` nor the npm package
contains kit code. the operator decides whether to accept these as test-only exceptions or to drop
the real-kit tests; the `licences` CI job stays red until then.

## 8. Results recorded on 2026-11-13

- Trivy and syft on all 12 platform images built from this tree and the 11 upstream images. All 12
  platform images pass the gate after three fixes: `github.com/klauspost/compress` 1.20.1 to 1.20.4
  (GO-2026-6865, called from the replayer), Caddy built with `golang.org/x/net` v0.62.0
  (CVE-2026-78661 and others), and `postgres-ha` without the unused `gosu` (Go 1.24.6 stdlib,
  one critical and ten high) and with the Debian `zlib1g` security update.
- Upstream images with fixable high findings that only their maintainers can fix: Umami 3.5.0
  (`undici` 6.28.0), HAProxy 3.4.7 (`zlib1g`), etcd 3.6.16, Prometheus 3.15.1, Alertmanager 0.34.2,
  Grafana 13.2.6, Gatus 5.37.0, node_exporter 1.12.1, postgres_exporter 0.20.1,
  prometheus-nats-exporter 0.20.3. The hourly workflow files these as issues; each one needs a pin
  bump to a fixed upstream release or a documented mitigation.
- govulncheck: no called vulnerability in either module after the fixes (the example app moved to
  `golang.org/x/net` v0.62.0). One uncalled module finding remains (`golang.org/x/crypto/openpgp`,
  GO-2026-5932, fixed only in a pseudo-version; the package is not imported).
- A throwaway build of `postgres-ha` with `gosu` left in fails the gate: 1 critical, 25 high,
  exit 1.
