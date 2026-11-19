---
title: "Operator API key revocation"
sidebarTitle: "Operator key revocation"
description: "Revoke an operator API key and confirm that it no longer works."
---

Operator API keys (`sa_op_` plus 40 characters) give machine read access to the metrics API
(`/api/metrics/v1/...` on `app.<BASE_DOMAIN>`), either to every tenant or to one. They are stored
only as SHA-256 hashes, expire after at most 365 days, and nothing caches a key, so revocation
takes effect on the next request. Creating and using keys is the operator guide page
[Operator API keys](../operators/api-keys.md); the HTTP routes are sections 9.2 and 9.3 of the
[control plane API reference](../reference/control-plane-api.md).

Revoke a key when it leaked (seen in a log, a repository, a ticket), when the system that used it
is retired, or when the person who created it leaves and the key's purpose is unclear.

## Steps

1. **Find the key.** In the console operator area open **Access keys**, or list the keys
   through the API with an operator's console session (its cookie and CSRF rules are section 1.2
   of the control plane API reference):

   `GET /api/operator/api-keys`

   A leaked key is identified by its `prefix` field (the first characters of the key, for example
   `sa_op_` and four more), never by sending the full key anywhere.

2. **Revoke it** with **Revoke** in the key's row menu, then **Revoke** in the confirmation, or:

   `DELETE /api/operator/api-keys/<key id>`

   Expected: `204`. Revoking an already revoked key is also `204` and changes nothing. The audit
   log gets `operator_api_key.revoke` with target `operator_api_key:<key id>`.

3. **Confirm** the key is refused: the next request with it answers `401`. The key object now
   has `"status": "revoked"` and a `revoked_at` time.

4. **Look at what it did.** `GET /api/operator/api-keys/<key id>/usage?from=<YYYY-MM-DD>&to=<YYYY-MM-DD>`
   lists accepted and refused requests per hour. Refusals include requests after revocation, so a
   leaked key still in use shows up as refusals. `last_used_at` is its last accepted request.

5. **Replace it** if the consumer is legitimate: create a new key with the narrowest scope that
   works (`tenant_id` set when one tenant is enough) and a short `expires_in_days`. The new key
   is shown once.

6. **If a key leaked**, also read the audit list for the period
   (`GET /api/operator/audit`) and follow [security-incident.md](security-incident.md) when the
   leak was not a simple mistake.

The monitoring probe has its own scoped key (`probe_machine_api_key` in the SOPS file, made by
`platformctl probe-tenant create`). Revoking it makes the dashboard check of the status page fail
until a new key is created and written to the SOPS file ([secret-rotation.md](secret-rotation.md)).

## Exercised

| Date | What ran | Result |
|---|---|---|
| 2026-11-13 | `go test -race -run 'TestMachineAPI$' ./internal/controlplane/operator/` against real PostgreSQL, NATS and Umami containers, through the same routes this runbook calls | 13 of 13 subtests pass, among them `operators manage keys and nobody else does`, `a revoked key is refused on the very next request`, `an expired key is refused` and `no plaintext key is stored anywhere` |
