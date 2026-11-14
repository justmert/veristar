# Adoption evidence, 2026-11-21

Written by `cmd/adoptionreport` from the machine metrics API. No number in this file was typed
by hand; each one traces to a request listed under [Sources](#sources). Do not edit the
numbers. To refresh them, run the command again ([README.md](README.md), section 6).

| | |
|---|---|
| Generated at | 2026-11-21T08:00:00Z |
| Machine API | `http://contract.test/api/metrics/v1` |
| Range | 2026-11-01 to 2026-11-20 (20 UTC days) |
| Threshold | 9 consecutive UTC days with accepted page views (beyond the RFP) |

## Summary

| Project | Tenant id | Status | Enrolled | First accepted page view in range | Longest run | Meets 9 days | Days with API requests |
|---|---|---|---|---|---|---|---|
| Project A | `0190a000-0000-7000-8000-00000000000a` | active | 2026-10-01 | 2026-11-01 | 9 days (2026-11-12 to 2026-11-20) | yes | 2 of 20 |
| Project \| B | `0190a000-0000-7000-8000-00000000000b` | active | 2026-10-01 | 2026-11-13 | 8 days (2026-11-13 to 2026-11-20) | no | 0 of 20 |

## How to read this file

- **Accepted page view.** The gateway accepts an event only after its challenge proof
  verified, and the replayer stores it in the site's primary or quarantine band
  (`docs/reference/browser-proof.md`). A day counts towards a run when the primary band has
  at least one page view. Quarantined page views are shown beside it and never counted.
- **Days not aggregated.** The API leaves out a day the aggregation task never read. Such a
  day breaks a run, the same as a day without page views, and is marked in the daily table.
- **Visitors** are unique per UTC day and site, so visitors over the range are the sum of the
  daily numbers, and breakdown values are visitor-days (`docs/reference/web-metrics.md`).
- **Session duration** is the average visit duration, total time over visits; **bounce rate**
  is bounces over visits. Both are over the days with accepted page views in the range.
- **API percentiles** are per day. The API never combines percentiles across days, so the
  range has request totals only. Routes are shown for the newest day with requests.

## Project A

Tenant `0190a000-0000-7000-8000-00000000000a`, slug `project-a`, status active. Sites: 2, verified: 1, with a stellar.toml: 1.

### Web: days with accepted page views

| | |
|---|---|
| Days with accepted page views | 18 of 20 |
| Longest run | 9 days (2026-11-12 to 2026-11-20) |
| Run ending on the last day of the range | 9 days (2026-11-12 to 2026-11-20) |
| Days the aggregation never read | 1 |
| 9-day threshold | met |

### Web: the seven metrics over the range (primary band)

| RFP metric | Value |
|---|---|
| Page views | 355 |
| Unique visitors (sum of daily unique visitors) | 173 |
| Session duration (average visit) | 62 s over 173 visits |
| Bounce rate | 47.4% |
| Referrers | 1 distinct, top below |
| Countries | 2 distinct, top below |
| Devices | 0 distinct, top below |
| Browsers | 0 distinct, top below |
| Operating systems | 0 distinct, top below |

Quarantined page views in the range, not counted above: 19.

**Referrers** (visitor-days, top 10)

| Key | Visitor-days |
|---|---|
| a\|b.example | 1 |

**Countries** (visitor-days, top 10)

| Key | Visitor-days |
|---|---|
| DE | 5 |
| NG | 4 |

**Devices** (visitor-days, top 10)

None in the range.

**Browsers** (visitor-days, top 10)

None in the range.

**Operating systems** (visitor-days, top 10)

None in the range.

### Web: per day

| Day | Page views | Visitors | Visits | Bounce rate | Session duration | Quarantined page views | Counts |
|---|---|---|---|---|---|---|---|
| 2026-11-01 | 10 | 5 | 5 | 40.0% | 60 s | 1 | yes |
| 2026-11-02 | 11 | 5 | 5 | 40.0% | 66 s | 1 | yes |
| 2026-11-03 | 12 | 6 | 6 | 50.0% | 60 s | 1 | yes |
| 2026-11-04 | 13 | 6 | 6 | 50.0% | 65 s | 1 | yes |
| 2026-11-05 | 14 | 7 | 7 | 42.9% | 60 s | 1 | yes |
| 2026-11-06 | 0 | 0 | 0 | none (no visits) | none (no visits) | 1 | no |
| 2026-11-07 | 16 | 8 | 8 | 50.0% | 60 s | 1 | yes |
| 2026-11-08 | 17 | 8 | 8 | 50.0% | 64 s | 1 | yes |
| 2026-11-09 | 18 | 9 | 9 | 44.4% | 60 s | 1 | yes |
| 2026-11-10 | 19 | 9 | 9 | 44.4% | 63 s | 1 | yes |
| 2026-11-11 | not aggregated | | | | | | no |
| 2026-11-12 | 21 | 10 | 10 | 50.0% | 63 s | 1 | yes |
| 2026-11-13 | 22 | 11 | 11 | 45.5% | 60 s | 1 | yes |
| 2026-11-14 | 23 | 11 | 11 | 45.5% | 63 s | 1 | yes |
| 2026-11-15 | 24 | 12 | 12 | 50.0% | 60 s | 1 | yes |
| 2026-11-16 | 25 | 12 | 12 | 50.0% | 62 s | 1 | yes |
| 2026-11-17 | 26 | 13 | 13 | 46.2% | 60 s | 1 | yes |
| 2026-11-18 | 27 | 13 | 13 | 46.2% | 62 s | 1 | yes |
| 2026-11-19 | 28 | 14 | 14 | 50.0% | 60 s | 1 | yes |
| 2026-11-20 | 29 | 14 | 14 | 50.0% | 62 s | 1 | yes |

### API: volume, status codes and latency

| | |
|---|---|
| Days with requests | 2 of 20 |
| Longest run of days with requests | 2 days (2026-11-19 to 2026-11-20) |
| Requests | 150 |
| Status below 400 | 139 |
| Status 4xx | 10 (6.7%) |
| Status 5xx | 1 (0.7%) |
| Note | some days were re-bucketed inexactly, so their percentiles are approximate |

| Day | Requests | Below 400 | 4xx | 5xx | p50 | p95 | Source | Services |
|---|---|---|---|---|---|---|---|---|
| 2026-11-19 | 100 | 89 | 10 | 1 | 20.0 ms | 1.500 s | metric | 1 |
| 2026-11-20 | 50 | 50 | 0 | 0 | 10.0 ms | >10 s | mixed, approximate | 2 |

**Routes on 2026-11-20**, by requests

| Service | Method | Route | Requests | 4xx | 5xx | p50 | p95 | Source |
|---|---|---|---|---|---|---|---|---|
| svc | GET | /a/{id} | 30 | 0 | 0 | 10.0 ms | >10 s | metric |
| svc | GET | (empty) | 20 | 0 | 0 | 10.0 ms | 20.0 ms | span |

## Project \| B

Tenant `0190a000-0000-7000-8000-00000000000b`, slug `project-\|-b`, status active. Sites: 2, verified: 1, with a stellar.toml: 1.

### Web: days with accepted page views

| | |
|---|---|
| Days with accepted page views | 8 of 20 |
| Longest run | 8 days (2026-11-13 to 2026-11-20) |
| Run ending on the last day of the range | 8 days (2026-11-13 to 2026-11-20) |
| Days the aggregation never read | 12 |
| 9-day threshold | not met: the longest run is 8 of 9 days |

### Web: the seven metrics over the range (primary band)

| RFP metric | Value |
|---|---|
| Page views | 24 |
| Unique visitors (sum of daily unique visitors) | 8 |
| Session duration (average visit) | 60 s over 8 visits |
| Bounce rate | 0.0% |
| Referrers | 0 distinct, top below |
| Countries | 0 distinct, top below |
| Devices | 0 distinct, top below |
| Browsers | 0 distinct, top below |
| Operating systems | 0 distinct, top below |

Quarantined page views in the range, not counted above: 0.

**Referrers** (visitor-days, top 10)

None in the range.

**Countries** (visitor-days, top 10)

None in the range.

**Devices** (visitor-days, top 10)

None in the range.

**Browsers** (visitor-days, top 10)

None in the range.

**Operating systems** (visitor-days, top 10)

None in the range.

### Web: per day

| Day | Page views | Visitors | Visits | Bounce rate | Session duration | Quarantined page views | Counts |
|---|---|---|---|---|---|---|---|
| 2026-11-01 | not aggregated | | | | | | no |
| 2026-11-02 | not aggregated | | | | | | no |
| 2026-11-03 | not aggregated | | | | | | no |
| 2026-11-04 | not aggregated | | | | | | no |
| 2026-11-05 | not aggregated | | | | | | no |
| 2026-11-06 | not aggregated | | | | | | no |
| 2026-11-07 | not aggregated | | | | | | no |
| 2026-11-08 | not aggregated | | | | | | no |
| 2026-11-09 | not aggregated | | | | | | no |
| 2026-11-10 | not aggregated | | | | | | no |
| 2026-11-11 | not aggregated | | | | | | no |
| 2026-11-12 | not aggregated | | | | | | no |
| 2026-11-13 | 3 | 1 | 1 | 0.0% | 60 s | 0 | yes |
| 2026-11-14 | 3 | 1 | 1 | 0.0% | 60 s | 0 | yes |
| 2026-11-15 | 3 | 1 | 1 | 0.0% | 60 s | 0 | yes |
| 2026-11-16 | 3 | 1 | 1 | 0.0% | 60 s | 0 | yes |
| 2026-11-17 | 3 | 1 | 1 | 0.0% | 60 s | 0 | yes |
| 2026-11-18 | 3 | 1 | 1 | 0.0% | 60 s | 0 | yes |
| 2026-11-19 | 3 | 1 | 1 | 0.0% | 60 s | 0 | yes |
| 2026-11-20 | 3 | 1 | 1 | 0.0% | 60 s | 0 | yes |

### API: volume, status codes and latency

No API requests in the range.

## Sources

Every answer this file was computed from, in request order. The request id is the one the
control plane logged; the hash is the SHA-256 of the response body.

| # | Request | Status | Request id | SHA-256 of the body |
|---|---|---|---|---|
| 1 | `/tenants` | 200 | `req-2` | `95a459f1c35d23e9049c598105b89dea210e15569ebb7a8d8da484cb6d915619` |
| 2 | `/tenants?cursor=c1` | 200 | `req-2` | `3c05fc69a596247888fe4019fb0e66c3b1fded20dab13b6547ed04922862c7f9` |
| 3 | `/web/daily?from=2026-11-01&tenant=0190a000-0000-7000-8000-00000000000a&to=2026-11-20` | 200 | `req-3` | `b5c0e1e4d973d60035d5eb8bc5035502d220d4af1a443d34f1927de598b7e4d7` |
| 4 | `/web/daily?cursor=p2&from=2026-11-01&tenant=0190a000-0000-7000-8000-00000000000a&to=2026-11-20` | 200 | `req-4` | `e22733a4a36b0c635068dd976a0b9ef547cea847e57dd7b956816b683a03113c` |
| 5 | `/web/dimensions?from=2026-11-01&tenant=0190a000-0000-7000-8000-00000000000a&to=2026-11-20` | 200 | `req-5` | `889d9306314ffe063c6def63066d06b341ad67619a1ffeec26815c0d6df72026` |
| 6 | `/api/daily?from=2026-11-01&tenant=0190a000-0000-7000-8000-00000000000a&to=2026-11-20` | 200 | `req-6` | `ff2c32e848f9bd6d111cccc865c1e6e80f3e91b7144609edfed496d5e32b6c8b` |
| 7 | `/api/routes/daily?from=2026-11-20&tenant=0190a000-0000-7000-8000-00000000000a&to=2026-11-20` | 200 | `req-7` | `faa0f691c17d581cbe2344512ed9f0b0e418405250e18327c88f46533b82ddc4` |
| 8 | `/web/daily?from=2026-11-01&tenant=0190a000-0000-7000-8000-00000000000b&to=2026-11-20` | 200 | `req-8` | `3650ea6efd98c5ffa965bb8a7b71630a551cb9faa36cba09f626d51e5f60bd24` |
| 9 | `/web/dimensions?from=2026-11-01&tenant=0190a000-0000-7000-8000-00000000000b&to=2026-11-20` | 200 | `req-9` | `3849aa77ee2f6f24891793b9c0892ee4530ac3fcecc2053ee9a6e12c1ddf2648` |
| 10 | `/api/daily?from=2026-11-01&tenant=0190a000-0000-7000-8000-00000000000b&to=2026-11-20` | 200 | `req-10` | `3849aa77ee2f6f24891793b9c0892ee4530ac3fcecc2053ee9a6e12c1ddf2648` |
