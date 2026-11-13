---
title: "DB-IP monthly update"
description: "Update the monthly DB-IP country file baked into the gateway image."
---

The gateway turns a truncated client address into a country code with a local copy of DB-IP's
"IP to Country Lite" file (CC BY 4.0, attribution required). DB-IP publishes one file per month.
The file is baked into the gateway image; the image names the month it carries
(`/geo/dbip-country-lite.mmdb.month`). Details and the licence terms:
[../reference/geo.md](../reference/geo.md).

Alert: `DBIPFileOld` fires when the file in use was built more than 45 days ago
([alerts.md](alerts.md)). Do the update every month, early in the month once DB-IP has published.

## Steps

1. **Fetch and check the new file locally:**

   ```sh
   make geo-fetch GEO_MONTH=<YYYY-MM>
   ```

   Expected:

   ```
   fetch-dbip: downloading https://download.db-ip.com/free/dbip-country-lite-<YYYY-MM>.mmdb.gz
   dbipcheck: ok, built <YYYY-MM>-01T..Z
   fetch-dbip: wrote .../.scratch/scratch/geo/dbip-country-lite.mmdb (<YYYY-MM>)
   ```

   Before DB-IP has published the month, `curl` reports `404` and the target fails; nothing is
   written. Never take another month silently: pass the previous month explicitly if you must.

2. **Run the geo tests against it** (they fail if the truncation error rate rises above 0.5%):

   ```sh
   DBIP_MMDB=.scratch/scratch/geo/dbip-country-lite.mmdb go test -race -count=1 -run TestGeo ./internal/gateway/
   ```

   Expected: `ok  github.com/justmert/veristar/internal/gateway`.

3. **Build the release with the new month.** `release-images.yml` builds the gateway with
   `DBIP_MONTH` set to the month of the release tag's commit date, so a release tagged on a
   commit from this month carries this month's file. Tag a platform release after DB-IP has
   published, and roll it out:

   ```sh
   make staging-deploy ROLLING=1 SA_IMAGE_TAG=<release> SA_RELEASE_MANIFEST=<release-images.json>
   ```

   For a local or test image, `docker build -f deploy/images/gateway.Dockerfile --build-arg DBIP_MONTH=<YYYY-MM> .`
   (or `make gateway-up`, which passes the current month).

4. **Check** a node serves the new month: `DBIPFileOld` resolves, and the gateway metric
   `gateway_geo_build_timestamp_seconds` shows the new build time in Prometheus.

5. **Attribution** stays as it is: the image ships `DBIP-ATTRIBUTION.txt` next to the file, and
   the console and docs carry the DB-IP link ([../reference/geo.md](../reference/geo.md)).

A running gateway can also load a replaced file without restart on `SIGHUP` (the file is replaced
by renaming over the old one; if the new file does not open, the error is logged and the old one
stays). The production images do not mount the file from the host, so the release path above is
the normal one.

## Exercised

| Date | What ran | Result | Source |
|---|---|---|---|
| 2026-11-12 | this stage: first fetch, file shape and truncation measured | 2026-11 file, 1,410,667 networks; truncation error 0.0916% of IPv4 addresses | [../reference/geo.md](../reference/geo.md) |
| 2026-11-13 | this stage, steps 1 and 2 locally: `make geo-fetch GEO_MONTH=2026-11`, then the geo tests | `dbipcheck: ok, built 2026-11-01T02:47:39Z`, file written with month `2026-11`; `go test -run TestGeo` ok in 8.5 s | this page |
| 2026-11-13 | this stage: `scripts/fetch-dbip.sh <dir> 2026-12` before December's publication | `curl: (22) The requested URL returned error: 404`, exit 22, nothing written | this page |
| 2026-11-13 | this stage: `SIGHUP` reload on a local gateway (with the tracker revocation rehearsal) | `geo file and revoked tracker list reloaded` logged; a broken file left the previous state in use | [tracker-release.md](tracker-release.md) |

Not yet exercised: step 3 through the release workflow (GitHub Actions fails at start-up on the
account) and step 4 on a deployed node.
