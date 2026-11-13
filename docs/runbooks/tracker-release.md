---
title: "Tracker release and revocation"
description: "Release a new immutable tracker version, and revoke one if needed."
---

The browser tracker `sdk.js` is released as immutable files:
`packages/tracker/releases/<version>/sdk.js` plus one entry in
`packages/tracker/releases/manifest.json` (size, SHA-384, the `integrity` value tenants paste).
A released file never changes; a fix is a new version. `packages/tracker/releases/revoked.json`
is the only mutable file. The gateway serves `GET /v/<version>/sdk.js` from the copy baked into
its image and answers **410** for a revoked version; the control plane, from its own baked copy,
refuses to show a snippet for one (`tracker_version_revoked`). How tenants check a file:
[../security/verify-tracker.md](../security/verify-tracker.md).

## Release a new version

Needs Node 26.13.1 and pnpm 12.18.0.

1. Set `version` in `packages/tracker/package.json` to the new version. The release script
   refuses anything else.

2. Build and record the release:

   ```sh
   pnpm --filter tracker release <x.y.z>
   ```

   Expected: `gzip size <old> -> <new> (<n>%)` and the new manifest entry as JSON. The script
   refuses a version that exists or is not newer than the newest, and never overwrites a file.

3. Check the release directory, a byte-identical rebuild and the size budget:

   ```sh
   pnpm --filter tracker verify-releases
   ```

   Expected last line: `releases verified against HEAD`. CI runs the same against the change's
   base and fails if any committed release file or entry changed.

4. Run the tracker's tests and the browser suites that pin a version: `pnpm --filter tracker test`,
   then `make gateway-it` and `make webpath` (see the Makefile for their prerequisites).

5. Commit, merge, and tag `tracker-v<x.y.z>`. `release-tracker.yml` checks that the tag names
   the newest committed release and publishes it (attestation and npm publication wait for the
   GitHub Actions account issue and an operator input; [../security/verify-tracker.md](../security/verify-tracker.md)
   sections 3 and 6).

6. Ship the files: the gateway and control plane images copy `packages/tracker/releases`, so tag
   a platform release and roll it out (`make <env>-deploy ROLLING=1 ...`). Until then the new
   version answers 404 at the edge.

7. Tell tenants: the console offers the newest non-revoked version; existing snippets keep the
   version they pinned. Tenants upgrade on purpose.

## Revoke a version

A version has a flaw serious enough that pages must stop running it, for example a tracker
that sends more than the wire format allows, or a doubt about how a file was built.

1. Add the version with a reason to `packages/tracker/releases/revoked.json`:

   ```json
   {
     "revoked": [
       {"version": "<x.y.z>", "reason": "<one line, shown in logs and to operators>"}
     ]
   }
   ```

   Leave `releases/<version>/` and its manifest entry in place: removing them would break the
   immutability check and the record of what was served.

2. `pnpm --filter tracker verify-releases`, then commit and tag a platform release, and roll it
   out to every edge and core node. Pages that load the version get 410 and record nothing;
   snippet requests for it get `410 tracker_version_revoked`. A page that still runs the file from
   its cache or a self-hosted copy keeps sending: from 1.2.0 those events name the release and
   the gateway refuses them with 410 (`rejected_revoked_version` on the site's counters); events
   of 1.0.0 and 1.1.0 carry no release name and still pass.

3. On a node that already runs an image with the new list, a reload without restart is
   possible: `SIGHUP` makes the gateway read `revoked.json` (and the DB-IP file) again, and the
   control plane reads the file on every snippet request. Inside an image the file only changes
   with a new image, so in practice step 2 is the path.

4. Tell the affected tenants which version to move to and why. Make sure a non-revoked version
   exists first: if every version is revoked, the console answers
   `every tracker version is revoked`.

## Exercised

| Date | What ran | Result | Source |
|---|---|---|---|
| 2026-11-12 | Releases 1.0.0 and 1.1.0 made with `pnpm --filter tracker release`; 1.1.0 rebuilt byte-identical in a clean Node container | released; immutability and rebuild checks pass | plan status, phases 3 and 14 |
| 2026-11-13 | this stage: `pnpm --filter tracker verify-releases` | `releases verified against HEAD` | this page |
| 2026-11-13 | this stage: revocation on a local gateway (`dist/bin/gateway` on the development stack's NATS, `TRACKER_RELEASE_DIR` pointing at a scratch copy of `packages/tracker/releases`): 1.0.0 added to `revoked.json`, `SIGHUP` | before: 1.0.0 200, 1.1.0 200; after `SIGHUP`: 1.0.0 **410**, 1.1.0 200; a broken `revoked.json` plus `SIGHUP` logged `reload failed, previous geo file and revoked list stay in use` and 1.0.0 stayed 410 | this page |
| 2026-11-13 | this stage: `go test -run TestReleases ./internal/gateway/` | `TestReleasesServeCommittedTracker` and `TestReleasesRevokedAndTampered` pass | this page |

Not yet exercised: a release or revocation rolled out through images to staging or production,
and the tag workflow (GitHub Actions fails at start-up on the account, plan status this stage).
