---
title: "DB-IP monthly update"
description: "Update the monthly DB-IP country file baked into the gateway image."
---

The gateway turns a truncated client address into a country code with a local copy of DB-IP's
"IP to Country Lite" file (CC BY 4.0, attribution required). DB-IP publishes one file per month.
The file is baked into the gateway image; the image names the month it carries
(`/geo/dbip-country-lite.mmdb.month`). Details and the licence terms:
[../reference/geo.md](../reference/geo.md).

The same month pins the network data the gateway classifies client networks with (DB-IP ASN Lite,
ipverse as-metadata categories, Tor exit addresses; [../reference/geo.md](../reference/geo.md),
"Network class"). `make geo-fetch` and the image build fetch both, so this update renews both.

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
   fetch-dbip: wrote .../.scratch/geo/dbip-country-lite.mmdb (<YYYY-MM>)
   ```

   The target then runs `scripts/fetch-network.sh`, which ends with
   `fetch-network: wrote .../.scratch/geo (<YYYY-MM>, as-metadata <commit>, tor exits of <previous month>)`.

   Before DB-IP has published the month, `curl` reports `404` and the target fails; nothing is
   written. The script never falls back to another month on its own: to use the previous month,
   pass it explicitly.

2. **Run the geo tests against it** (they fail if the truncation error rate rises above 0.5%):

   ```sh
   DBIP_MMDB=.scratch/geo/dbip-country-lite.mmdb go test -race -count=1 -run 'TestGeo|TestNetwork' ./internal/gateway/
   ```

   Expected: `ok  github.com/justmert/veristar/internal/gateway`.

3. **Build the release with the new month.** `release-images.yml` builds the gateway with
   `DBIP_MONTH` set to the month of the release tag's commit date, so a release tagged on a
   commit from this month carries this month's file. Tag a platform release after DB-IP has
   published, and roll it out:

   ```sh
   make <env>-deploy ROLLING=1 SA_IMAGE_TAG=<release> SA_RELEASE_MANIFEST=<release-images.json>
   ```

   `<env>` is `staging` or `production`.

   For a local or test image, `docker build -f deploy/images/gateway.Dockerfile --build-arg DBIP_MONTH=<YYYY-MM> .`
   (or `make gateway-up`, which passes the current month). To build from the files that
   `make geo-fetch` already wrote instead of downloading them again, add
   `--build-context geo=.scratch/geo`; the build refuses files of another month and opens them
   with the gateway's own loaders. The stack tests and `infra/local/build-images.sh` do this when
   `GATEWAY_GEO_DIR` names that directory, as the e2e workflow does.

4. **Check** a node serves the new month: `DBIPFileOld` resolves, and the gateway metrics
   `gateway_geo_build_timestamp_seconds` and `gateway_network_build_timestamp_seconds` show the
   new build times in Prometheus.

5. **Attribution** stays as it is: the image ships `DBIP-ATTRIBUTION.txt` and
   `NETWORK-ATTRIBUTION.txt` next to the files, and
   the console and docs carry the DB-IP link ([../reference/geo.md](../reference/geo.md)).

A running gateway can also load a replaced file without restart on `SIGHUP` (the file is replaced
by renaming over the old one; if the new file does not open, the error is logged and the old one
stays). The production images do not mount the file from the host, so the release path above is
the normal one.

## Exercised

| Date | What ran | Result | Source |
|---|---|---|---|
| 2026-11-12 | First fetch; file shape and truncation error measured | 2026-11 file, 1,410,667 networks; truncation error 0.0916% of IPv4 addresses | [../reference/geo.md](../reference/geo.md) |
| 2026-11-13 | Steps 1 and 2 locally: `make geo-fetch GEO_MONTH=2026-11`, then the geo tests | `dbipcheck: ok, built 2026-11-01T02:47:39Z`, file written with month `2026-11`; `go test -run TestGeo` ok in 8.5 s | this page |
| 2026-11-13 | `scripts/fetch-dbip.sh <dir> 2026-12` before December's publication | `curl: (22) The requested URL returned error: 404`, exit 22, nothing written | this page |
| 2026-11-13 | `SIGHUP` reload on a local gateway | `geo file and revoked tracker list reloaded` logged; a broken file left the previous state in use | [tracker-release.md](tracker-release.md) |
