---
title: "On-chain activity and exports"
description: "Read projects' ledger activity next to web and API usage, and export the views you decide on."
---

Every project can declare the Stellar accounts and Soroban contracts it operates and prove that it
controls them. The operator views show their ledger activity next to web and API usage, and every
global and compare view exports to CSV exactly as shown. The contracts are sections 15 and 16 of
[control-plane-api.md](../reference/control-plane-api.md); the sources and their limits are on
[On-chain data sources](../reference/onchain-sources.md).

## Read the on-chain column

**Global web** and **Global API** carry an **On-chain** column: the operations of the project's
declared ids over the range, with how many of its ids are verified and, when some days have no data,
how many id-days are missing. Hover the cell for transactions and failing reads. A project without
declared ids shows a dash.

- **Verified** means the project proved control: a SEP-53 signature or a data entry for an account,
  or for a contract its stored admin or its deployer being one of the project's verified accounts.
  Give unverified ids no weight in a decision; anyone can declare any public id.
- **Missing** days were not read, for a reason the detail names. They are never zeros. Accounts
  are read back 90 days at the first read; contracts only from about a week before they were
  declared, because public Stellar RPC servers keep events for about seven days.
- Operations of an account count every successful operation that involves it; operations of a
  contract count the operations in which it emitted an event. Counterparties are distinct per day.

The column is not a sort key. Filter and sort by the web or API columns, then read it.

## Read a project's detail

**Projects**, then a project: the **On-chain** section lists each declared id with its proof, its
daily operations (dashed cells for missing days) and the reason for every missing day, and shows
whether the project's stellar.toml and its accounts' home domain link the verified domain both
ways. **Compare** adds an **On-chain** section with each project's daily operations over the range.

## Export what you see

- **Global web** and **Global API**: **Export CSV** under the table downloads exactly the rows the
  table shows, with the same range, filters, sort and order. Load more rows first to export more,
  up to 5,000.
- **Compare**: **Export web CSV** and **Export API CSV** download one row per project and day.

A value that is not known is an empty cell, never 0. Each file carries the on-chain columns, and
each export writes an `export.csv` row to the audit log with the query it answered.

## What projects see

Each project has a **What SCF sees** page with its own row of these views, computed by the same
queries for the same range, with its integrity grade. It sees nothing about other projects.

## Machine API and AI clients

- The machine API serves `GET /api/metrics/v1/onchain/ids` (declared ids with proofs and read
  state) and `GET /api/metrics/v1/onchain/daily` (the stored daily rows), with the key scopes of
  [API keys](api-keys.md).
- The operator MCP connection has `get_tenant_onchain`; the overviews and compares it reads carry
  the on-chain summary per project.

## When reads fail

The jobs task `chain-activity` runs hourly and reads each id once a day. Its metric
`jobs_chain_reads_total{kind, result}` counts reads by result. A burst of `rate_limited` or
`source_unavailable` means the ledger API refused or failed; the next runs catch up, because a day
is stored only once it was read whole. Set `STELLAR_HORIZON_URL` or `STELLAR_RPC_URL` to another
provider to change the source.
