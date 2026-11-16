---
title: "Signed usage reports"
description: "The report format, its canonical JSON form, the SEP-53 signature and how to verify a report with any tool."
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
  "report": { "format": "veristar-usage-report", "version": 1, "...": "..." },
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
2^53 - 1, which every JCS implementation writes as the same decimal digits, and every ratio,
share, percentile and latency is a decimal string with six digits after the point, such as
`"0.166667"`. So any RFC 8785 library in any language reproduces the signed bytes exactly. The
`report` member of a file Veristar serves is already in this form.

## What a report holds

| Member | Content |
|---|---|
| `format`, `version` | `veristar-usage-report`, `1` |
| `id` | the report's id, also the last part of its link |
| `issuer` | `name`, `console` (the console origin), `stellar_toml` (where the keys are published), `signing_key` (the signer, which must equal `signature.signer`) |
| `project` | `id`, `slug`, `name` |
| `period` | `from`, `to` (UTC days, both included, all closed before the report was made), `days` |
| `generated_at` | RFC 3339 UTC time of generation |
| `methods` | the version of every definition behind the numbers: `format`, `web_metrics`, `api_metrics`, `onchain`, `cohort`, `integrity_rules`, `route_normaliser`, and `platform`, the release that made the report |
| `web` | primary-band totals (page views, visitors, visits, bounces, total visit seconds, bounce rate, average visit seconds), the quarantine band, gateway requests and rejections, sites and verified sites, and a daily series |
| `api` | requests, 4xx and 5xx with their rates, p50, p95 and p99 latency, services, routes, source, and a daily series |
| `onchain` | declared ids and verified ids, operations, transactions, days not read, the two-way stellar.toml link, and a daily series |
| `integrity` | the grade over the period, its graded site days per grade, and every reason with its plain language text |
| `cohort` | the confirmed program, round, category and award date, the population of the percentiles, its size, and for visitors, page views, requests, error rate and on-chain operations the value, the previous period's value, the change and the percentile |
| `rules_met` | the operators' saved threshold rules the project met on the generation day |

The numbers are the ones the operator views and "What SCF sees" show for the same period:
the report reads them with the same SQL. A report holds aggregates only; no visitor, address,
user agent or member of the project is in it.

Method versions, version 1:

| Member | Definition |
|---|---|
| `web_metrics` | [web metrics](web-metrics.md): primary band, visitors unique per UTC day and site, summed over days and sites |
| `api_metrics` | section 8 of the control plane API: metric rows over span rows per service, percentiles from summed histogram buckets |
| `onchain` | section 15: Horizon operations of accounts, Stellar RPC events of contracts |
| `cohort` | section 19: change against the previous period of equal length, `cume_dist` percentiles in the cohort |
| `integrity_rules` | section 9 of [bot and inflation defences](../security/bot-and-inflation.md) |
| `route_normaliser` | the route normalisation of the collector |

A change of any definition raises its version, so a reader always knows which rules produced a
report.

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

It checks the signature, then reads `ACCOUNTS` of the stellar.toml the report names (HTTPS only)
and requires the signer to be listed. To check against a key you already hold instead, and fetch
nothing:

```sh
platformctl report verify report.json --signer GDG7D3Z267FHK3JHVPXF5SHTSQFEDLWYKEI5ECMSLDGKJUXPXZOJGF7O
```

The exit code is 0 for a valid report, 1 for an invalid one and 2 when the check could not run.

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
