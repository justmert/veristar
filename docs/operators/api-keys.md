# Operator API keys

Operator API keys give a downstream machine (a data warehouse job, a report
script, the monitoring prober) read access to aggregate metrics through the
machine API at `https://app.<BASE_DOMAIN>/api/metrics/v1/`. The contract is
`docs/reference/openapi/metrics-v1.yaml`; the rules around it are section 9 of
`docs/reference/control-plane-api.md`.

## Defaults and limits

| Setting | Value | Why |
|---|---|---|
| Scope | `metrics:read`, for every tenant or for one tenant | read-only aggregates; a one-tenant key for a consumer that needs only one project |
| Lifetime | 90 days by default, at most 365 | a forgotten key stops working on its own; the 365-day cap goes beyond the RFP |
| Rate | 10 requests per second per key and control plane instance, burst 20 | with two control plane instances behind the load balancer a key can reach up to twice that |
| Page size | 500 items, the default and the maximum | |
| Day ranges | at most 366 days per request | |
| Hour ranges | at most 744 hours for `/api/hourly`, 24 for `/web/hourly` | `/web/hourly` reads Umami live for every site, band and hour |

## Creating a key

An operator creates a key in the console, or with
`POST /api/operator/api-keys` and a body such as
`{"name": "warehouse", "scope": "metrics:read", "expires_in_days": 90}`. Add
`"tenant_id": "<id>"` for a key that reads one tenant. The answer holds the
key (`sa_op_` and 40 characters) once; it is never shown again. Only its
SHA-256 and its first 10 characters are stored. Creation is in the audit log
(`operator_api_key.create`).

## Using a key

```sh
curl -H "Authorization: Bearer $SA_KEY" \
  "https://app.<BASE_DOMAIN>/api/metrics/v1/web/daily?from=2026-11-01&to=2026-11-12"
curl -H "Authorization: Bearer $SA_KEY" \
  "https://app.<BASE_DOMAIN>/api/metrics/v1/api/daily?tenant=<id>"
```

Follow `next_cursor` until it is `null` to read every page. On 429 wait the
`Retry-After` seconds. The key works only under `/api/metrics/v1/`; a console
session cookie does not work there.

What each route returns:

- `/tenants`: the tenants the key may read, with site and verification counts.
- `/web/daily`, `/web/dimensions`: daily web numbers and breakdowns per tenant
  from the daily aggregates, the same numbers as the global web view.
- `/web/hourly`: one tenant's hourly web numbers read live from Umami,
  including the current hour.
- `/api/daily`, `/api/routes/daily`, `/api/hourly`: request counts, error
  rates and p50, p95, p99 per tenant (and per route), the same numbers as the
  global API view. Data derived from sampled traces is marked with
  `source: span`.

Visitors are unique per UTC day and site, so visitors over several days are
the sum of the daily numbers.

## Revoking a key

Revoke in the console or with `DELETE /api/operator/api-keys/<id>`. The next
request with the key is refused (401); nothing caches a key. Revocation is in
the audit log (`operator_api_key.revoke`). An expired key is refused the same
way.

## Usage

`GET /api/operator/api-keys/<id>/usage?from=<day>&to=<day>` lists requests
and refusals per hour. Refusals count requests with a revoked or expired key,
over the rate limit, or for a tenant outside the key's scope. `last_used_at`
on the key is the last accepted request.
