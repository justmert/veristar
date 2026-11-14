---
title: "Country lookup (DB-IP)"
description: "How the gateway derives a country code with a local DB-IP database, and nothing more precise."
---

The gateway turns a client address into an ISO 3166-1 alpha-2 country code with
a local copy of the DB-IP "IP to Country Lite" database. Only the country code
leaves the gateway. Region and city are never looked up. The address itself is
never logged, queued or stored.

Code: `internal/gateway/geo.go` (lookup), `internal/gateway/clientip.go`
(address handling), `scripts/fetch-dbip.sh` (download), `make geo-fetch`.

## Licence and attribution

Checked on 2026-11-12 at [https://db-ip.com/db/download/ip-to-country-lite](https://db-ip.com/db/download/ip-to-country-lite).

- Licence: Creative Commons Attribution 4.0 International (CC BY 4.0),
  [https://creativecommons.org/licenses/by/4.0/](https://creativecommons.org/licenses/by/4.0/).
- Requirement, in DB-IP's words: use is allowed "provided you give attribution
  to DB-IP.com for the data", and web applications must "include a link back to
  DB-IP.com on pages that display or use results from the database".
- The snippet DB-IP asks for:

  ```html
  <a href='https://db-ip.com'>IP Geolocation by DB-IP</a>
  ```

- No account or key is needed. The file is updated monthly. Download URL:
  `https://download.db-ip.com/free/dbip-country-lite-YYYY-MM.mmdb.gz`.

What this project does about it:

- `scripts/fetch-dbip.sh` writes `DBIP-ATTRIBUTION.txt` next to the database.
  The gateway image ships that file beside the database.
- Every page that shows country data must carry the link above: the console
  pages with country breakdowns, the operator dashboards and the public docs.
  Umami's own dashboard is run unmodified, so the console page that links into
  it and the tenant onboarding guide carry the attribution for it.
- The database is used unmodified. CC BY 4.0 does not restrict commercial use
  or require share-alike.

## File and record shape

Read with `github.com/oschwald/maxminddb-golang/v2` v2.9.0 from
`dbip-country-lite-2026-11.mmdb` (built 2026-11-01 02:47:39 UTC, 8,515,063
bytes uncompressed, 4,205,904 bytes gzipped).

Metadata:

| Field | Value |
|---|---|
| `database_type` | `DBIP-Country-Lite` |
| `ip_version` | 6 (IPv4 is held in the IPv4-mapped subtree) |
| `record_size` | 24 |
| `node_count` | 1,410,860 |
| `languages` | en, de, es, fr, ja, pt-BR, ru, zh-CN, fa, ko |
| `description.en` | `DB-IP.com - IP to Country` |

One record (the answer for `8.8.8.8`, names shortened):

```json
{
  "continent": { "code": "NA", "geoname_id": 6255149, "names": { "en": "North America", "...": "..." } },
  "country": {
    "geoname_id": 6252001,
    "is_in_european_union": false,
    "iso_code": "US",
    "names": { "en": "United States", "...": "..." }
  }
}
```

The gateway decodes only the path `country.iso_code`. Facts that shaped the code:

- 1,410,667 networks, 251 distinct `iso_code` values. One of them is `ZZ`,
  which DB-IP uses for ranges it cannot place. `ZZ` is not a country, so the
  gateway reports it as unknown (empty string). `XK` (Kosovo) appears and is
  passed through, because it is two capital letters and Umami accepts it.
- Private, loopback, documentation and link-local ranges (`10/8`, `127/8`,
  `192.0.2/24`, `::1`, `fe80::/10`, `2001:db8::/32`) have no record. The
  lookup reports them as unknown.
- `OpenGeo` refuses a file whose `database_type` is not `DBIP-Country-Lite` or
  whose `ip_version` is not 6, so a wrong download fails at start.
- Any other `iso_code` value that is not two capital letters is an error, not
  an empty country.

## Truncation and its cost

The address is cut to its /24 (IPv4) or /48 (IPv6) network before the lookup:
the last octet is zeroed, or the lower 80 bits for IPv6. A network narrower than
that can then be answered with the country of the network that holds the
truncated address. Measured on the 2026-11 file by `TestGeoTruncationMismatch`
(`internal/gateway/geo_test.go`) and a standalone run:

| Measure | IPv4 (/24) | IPv6 (/48) |
|---|---|---|
| Networks narrower than the truncation | 122,777 | 64,985 |
| Random sample, network drawn uniformly then a random address in it (200,000 addresses, standalone run) | 9,773 of 84,434 differ (11.57%) | 6,748 of 115,566 differ (5.84%) |
| Random sample in the test (20,000 addresses, fixed seed) | 976 of 8,549 differ (11.42%) | 669 of 11,451 differ (5.84%) |
| Exact, weighted by address space over the whole file | 3,391,023 of 3,702,258,680 addresses differ (0.0916%) | below 0.000001% |

How to read this:

- The uniform-by-network sample is dominated by the many tiny networks (single
  addresses, /29 and similar) that DB-IP carves out of larger blocks. Drawing
  networks uniformly gives each such sliver the same weight as a /8, so it
  overstates the error for real traffic.
- Weighted by address space, fewer than one IPv4 address in a thousand gets a
  different country. For IPv6 the difference is negligible, because nearly all
  allocations are /48 or wider.
- Real traffic is neither uniform over networks nor over addresses. The
  address-weighted figure is the closer bound for traffic from ordinary
  access networks. The cost is accepted, because a /24 or /48 cannot single
  out a household and the full address is never used for anything else.
- The test fails if the address-weighted rate rises above 0.5% in either family,
  so a future monthly file that changes this picture is noticed.

## Monthly update

1. `scripts/fetch-dbip.sh <dir> [YYYY-MM]` downloads the month's file (default:
   the current UTC month), decompresses it, opens it with the gateway's own
   reader (`scripts/dbipcheck`), checks that a public address resolves, and
   writes `dbip-country-lite.mmdb`, `dbip-country-lite.mmdb.month` and
   `DBIP-ATTRIBUTION.txt`. It never picks another month by itself: early on the
   1st, before DB-IP publishes, pass the previous month explicitly.
2. The image build runs the script. A running gateway picks up a replaced file
   (written to a temporary name and renamed over the old one) on `SIGHUP`
   (`Geo.ReloadOnSIGHUP`). If the new file does not open, the error is logged at
   error level and the previous file stays in use.

## Tests

The geo tests need the real file. They read its path from `DBIP_MMDB` (absolute,
or relative to the repository root) and skip with that instruction when it is
not set:

```sh
make geo-fetch
DBIP_MMDB=.scratch/geo/dbip-country-lite.mmdb go test -race ./internal/gateway/ -run 'TestGeo'
```

They take networks from the file itself (two countries per family, a network
narrower than the truncation whose country differs from its /24 or /48), so they
hold for every monthly release without hard-coded addresses.
