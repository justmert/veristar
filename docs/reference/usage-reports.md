---
title: "Signed usage reports"
description: "The report format, its canonical JSON form, the SEP-53 signature and how to verify a report with any tool."
icon: "file-signature"
---

A signed usage report states a project's web, API and on-chain usage for a period, with its
integrity grade, cohort and percentiles, and carries Veristar's signature over every byte of it.
Anyone holding the file can check, without an account and without Veristar's site, that Veristar
issued it unchanged. The routes are section 20 of the
[control plane API reference](control-plane-api.md); how a project creates and shares one is in
[share a signed report](../onboarding/signed-reports.md).

## The file

A report file is one JSON object with two members:

```json
{
  "report": { "format": "veristar-usage-report", "version": 3, "...": "..." },
  "signature": {
    "scheme": "sep-53",
    "signer": "GDG7D3Z267FHK3JHVPXF5SHTSQFEDLWYKEI5ECMSLDGKJUXPXZOJGF7O",
    "value": "<64-byte Ed25519 signature, standard base64>"
  }
}
```

`signature.value` is the [SEP-53](https://github.com/stellar/stellar-protocol/blob/master/ecosystem/sep-0053.md)
signature by `signature.signer` over the **canonical JSON bytes of `report`**. SEP-53 (status
Final, version 1.0.0) signs `SHA-256("Stellar Signed Message:\n" + message)` with the account's
Ed25519 key, so a signed message can never be mistaken for a transaction.

## The canonical form

The message is `report` written in the JSON Canonicalization Scheme,
[RFC 8785](https://www.rfc-editor.org/rfc/rfc8785) (JCS), as UTF-8 bytes:

- no whitespace between tokens;
- object members sorted by their names compared as UTF-16 code units;
- strings with the shortest escapes: `\"`, `\\`, `\b`, `\t`, `\n`, `\f`, `\r`, `\u00xx` (lowercase
  hex) for the other characters below U+0020, every other character as itself in UTF-8;
- `true`, `false`, `null` as written.

A report holds no fractional number. Every number is an integer between -(2^53 - 1) and
2^53 - 1, which every JCS implementation writes as the same decimal digits. A verifier reads a
number by its value, as RFC 8785 does: `12345.0` and `1.2345e4` both stand for `12345`, and a
value that is not such an integer is refused. A string holding a lone UTF-16 surrogate escape (such
as `"\ud800"` without its pair) is refused too, since it is no character; and every ratio,
share, percentile and latency is a decimal string with six digits after the point, such as
`"0.166667"`. So any RFC 8785 library in any language reproduces the signed bytes exactly. The
`report` member of a file Veristar serves is already in this form.

## What a report holds

| Member | Content |
|---|---|
| `format`, `version` | `veristar-usage-report`, `3` (reports created earlier carry `1` or `2`; see below) |
| `id` | the report's id, also the last part of its link |
| `issuer` | `name`, `console` (the console origin), `stellar_toml` (where the keys are published), `signing_key` (the signer, which must equal `signature.signer`) |
| `project` | `id`, `slug`, `name` |
| `period` | `from`, `to` (UTC days, both included, `to` at least four days before the day the report was made), `days` |
| `generated_at` | RFC 3339 UTC time of generation |
| `methods` | the version of every definition behind the numbers: `format`, `web_metrics`, `api_metrics`, `onchain`, `cohort`, `integrity_rules` (the list of integrity rule versions that graded the period's site days, ascending, empty without a graded day), `route_normaliser`, and `platform`, the release that made the report |
| `web` | `has_data` (false when no page view was counted, so the zeros mean no data), primary-band totals (page views, visitors, visits, bounces, total visit seconds, bounce rate, average visit seconds), the quarantine band, gateway requests and rejections, sites and verified sites, `coverage` (tracker file requests, those that sent Global Privacy Control, and the estimated share of visits not measured because of it), and a daily series |
| `api` | `has_data`, requests, 4xx and 5xx with their rates, p50, p95 and p99 latency, the services and routes that served application requests, source, `lossy` (a metric row of the period overflowed its route limit), `infrastructure_requests` (the requests every number leaves out), and a daily series |
| `onchain` | declared ids and verified ids; operations, transactions, days not read (`days_missing`) and a daily series of the verified ids only; `operations`, `transactions` and `days_missing` are `null` when they measure nothing, and `counts_reason` says why: `no_verified_id` (the project has no verified account or contract) or `nothing_read` (no ledger day of a verified id in the period was read); `counts_reason` is `null` when they hold numbers, so a `0` always means a read found no activity; the two-way stellar.toml link; `weekly_active_accounts` (`average` over the counted ISO weeks that lie whole inside the period, `latest`, `weeks_counted`, `weeks_missing`); `contracts`, one entry per verified contract: `strkey`, `days_read`, `days_no_call_tree`, `days_missing` and `functions` (`function`, `caller` (`account` or `contract`), `successful`, `failed` (`null` for calls of accounts), `peak_daily_accounts`, `days_with_calls`), as section 15.9 of the control plane API defines them |
| `integrity` | the grade over the period, its graded site days per grade, and every reason with its plain language text |
| `cohort` | the confirmed program, round, category and award date, the population of the percentiles, its size, and for visitors, page views, requests, error rate and on-chain operations the value, the previous period's value, the change and the percentile |
| `rules_met` | the operators' saved threshold rules the project met on the generation day |

The numbers are the ones the operator views and the project's **SCF review** page show for the same period:
the report reads them with the same SQL, and it holds every figure that page shows: the coverage
estimate, whether web and API data exist, whether API metrics were lossy, the infrastructure
requests left out, the weekly active accounts, and the calls per contract function. A report holds aggregates only; no visitor, address,
user agent or member of the project is in it.

### Format versions

| Version | Layout |
|---|---|
| `3` | the current layout above |
| `2` | reports created before format version 3: `onchain.contracts` and `onchain.counts_reason` are absent, `onchain.operations`, `onchain.transactions` and `onchain.days_missing` are always numbers (`0` without a verified id or a read day), and `methods.onchain` is `1` |
| `1` | reports created before format version 2: `methods.integrity_rules` is a single number, the rule version the jobs applied when the report was made, which is not necessarily the version that graded every day of the period; `web.has_data`, `web.coverage`, `api.has_data`, `api.lossy`, `api.infrastructure_requests` and `onchain.weekly_active_accounts` are absent |

A stored report never changes, so a version 1 or 2 report keeps its layout and still verifies:
the signature covers its own bytes, and the checks below read only members every version shares. A
reader of the numbers reads `version` first.

### Method versions

`methods` records the version of each definition below. A change of any definition raises its
version, so a reader always knows which rules produced a report. A new report carries the
current version of each:

| Member | Current version | Definition |
|---|---|---|
| `web_metrics` | `1` | [web metrics](web-metrics.md): primary band, visitors unique per UTC day and site, summed over days and sites |
| `api_metrics` | `2` | section 8 of the control plane API: metric rows over span rows per service, percentiles from summed histogram buckets. Version 2 leaves infrastructure requests (CORS preflights, health and probe routes, known health checkers; section 7.4) out of every number; version 1 counted them |
| `onchain` | `2` | section 15: Horizon operations of accounts, Stellar RPC events of contracts. Version 2 adds the calls per contract function of section 15.9 |
| `cohort` | `1` | section 19: change against the previous period of equal length, `cume_dist` percentiles in the cohort |
| `integrity_rules` | `2` | section 9 of [bot and inflation defences](../security/bot-and-inflation.md). Version 2 adds the page signals of tracker 1.3.0 (framed share and never-visible share) to version 1. The member lists the versions that graded the period's days, so a period graded across the change holds `[1, 2]` |
| `route_normaliser` | `2` | the route normalisation of the collector |

## The signing key

Veristar signs with one Stellar key that holds no funds and signs nothing else. The production
key is:

```text
GDG7D3Z267FHK3JHVPXF5SHTSQFEDLWYKEI5ECMSLDGKJUXPXZOJGF7O
```

It is published in three places that always agree:

- `https://app.veristar.app/.well-known/stellar.toml`, under the SEP-1 field `ACCOUNTS` ("a list
  of Stellar accounts that are controlled by this domain"). The current key comes first, earlier
  keys follow. The control plane writes this file from the key it signs with.
- The status page, [status.veristar.app](https://status.veristar.app), in its description and its
  **Report signing key** link.
- This page.

SEP-1's `SIGNING_KEY` field is not used: SEP-1 reserves it for SEP-10 and SEP-45 authentication
servers. A report signed by a key that has since been replaced stays valid: the earlier key stays
in `ACCOUNTS`, and the report page names it as an earlier key.

## Verify a report

A valid report needs both checks:

1. the signature verifies over the canonical bytes of `report` for `signature.signer`, and
   `report.issuer.signing_key` names the same account;
2. `signature.signer` is one of Veristar's report keys (above).

### In the browser

Open the report's link `https://app.veristar.app/r/<id>`. The page computes the canonical form
and checks the SEP-53 signature in your browser with WebCrypto, compares the signer with the
published keys, and offers the file as JSON.

### With platformctl

```sh
platformctl report verify report.json
```

It checks the signature, then reads `ACCOUNTS` of Veristar's stellar.toml,
`https://app.veristar.app/.well-known/stellar.toml`, which is built into `platformctl`, and requires
the signer to be listed there and the report's `issuer.stellar_toml` to name that same file. The
trusted file never comes from the report itself: anyone can sign a report with their own key and
name their own stellar.toml in it. For a report of another Veristar deployment, name that
deployment's file (HTTPS, path `/.well-known/stellar.toml`):

```sh
platformctl report verify report.json --toml https://app.analytics.example/.well-known/stellar.toml
```

To check against a key you already hold instead, and fetch nothing:

```sh
platformctl report verify report.json --signer GDG7D3Z267FHK3JHVPXF5SHTSQFEDLWYKEI5ECMSLDGKJUXPXZOJGF7O
```

The exit code is 0 for a valid report, 1 for an invalid one and 2 when the check could not run.
Every text it prints from the report, such as the project name, is quoted with escapes, so a name
holding a line break or a terminal control sequence cannot add a line to the verdict.

### With any SEP-53 implementation

Any RFC 8785 library and any SEP-53 implementation verify a report. With Node and the Stellar
JavaScript SDK (`npm install @stellar/stellar-sdk canonicalize`):

```js
import { readFileSync } from "node:fs";
import canonicalize from "canonicalize";
import { Keypair } from "@stellar/stellar-sdk";

const file = JSON.parse(readFileSync("report.json", "utf8"));
const message = Buffer.from(canonicalize(file.report), "utf8");
const signature = Buffer.from(file.signature.value, "base64");
const valid = Keypair.fromPublicKey(file.signature.signer).verifyMessage(message, signature);
console.log(valid && file.report.issuer.signing_key === file.signature.signer);
```

With Python and the Stellar Python SDK (`pip install stellar-sdk rfc8785`):

```python
import base64, json
import rfc8785
from stellar_sdk import Keypair

file = json.load(open("report.json", encoding="utf-8"))
message = rfc8785.dumps(file["report"])
signature = base64.b64decode(file["signature"]["value"])
Keypair.from_public_key(file["signature"]["signer"]).verify_message(message, signature)  # raises if invalid
assert file["report"]["issuer"]["signing_key"] == file["signature"]["signer"]
```

Then compare `file.signature.signer` with the published keys.
