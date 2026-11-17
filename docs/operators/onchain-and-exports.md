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
verified ids over the range, with how many of its declared ids are verified and, when some days have
no data, how many verified id-days are missing. Hover the cell for transactions and failing reads. A
project without declared ids shows a dash.

- **Verified** means the project proved control: a SEP-53 signature or a data entry for an account,
  or for a contract its stored admin or its deployer being one of the project's verified accounts.
  Anyone can declare any public id, so unverified ids count nowhere: the column's operations, the
  compare series, the on-chain key metric and its percentile, threshold rules on on-chain
  operations, What SCF sees and signed reports all read verified ids only, as weekly active
  accounts do. The project's detail section still lists every declared id with its own numbers.
- **Missing** days were not read, for a reason the detail names. They are never zeros. Accounts
  are read back 90 days at the first read; contracts only from about a week before they were
  declared, because public Stellar RPC servers keep events for about seven days.
- Operations of an account count every successful operation that involves it; operations of a
  contract count the operations in which it emitted an event. Counterparties are distinct per day.

The cell's last line is the project's **weekly active accounts**, averaged over the ISO weeks of
the range that have a count (see below). Hover the cell for the full line with the weeks counted
and missing.

The column is not a sort key. Filter and sort by the web or API columns, then read it.

## Read weekly active accounts

Weekly active accounts (WAA) follow the definition of the SCF Growth Hack official rules: "An
active account means the account that initiated at least one operation"
([SCF handbook, Growth Hack official rules, Top 5 Projects Selection](https://stellar.gitbook.io/scf-handbook/supporting-programs/growth-hack/official-rules)).
For a project, the platform counts the distinct accounts that initiated at least one successful
operation involving the project's **verified** accounts or contracts, per ISO week (Monday to
Sunday, UTC):

- for an account, the source account of every operation that involves it;
- for a contract, the source account of every operation in which the contract emitted an event.

An account counts once a week however many of the project's ids it used. The project's own
accounts count when they initiate operations. A relayed or sponsored invocation counts the account
that submitted the operation. The range figure is the mean of the counted weeks, as the Growth Hack
averages weekly counts, next to the latest counted week.

A week without a count shows its reason, never 0:

| Reason | Meaning |
|---|---|
| `week_not_complete` | the week is not over yet |
| `not_read_yet` | the week is over and a day is still being read; the week closes once it is read whole, or 14 days after its Monday at the latest |
| `no_verified_ids` | the project had no verified account or contract when the week closed |
| `day_over_read_cap` | a contract had more than 10,000 transactions with events in one day, so its initiators were not read whole |
| `before_source_history` | a day of the week was outside what the source still holds when the project declared the id; contract events stay readable for about seven days |
| `week_not_counted` | the week ended before the platform counted weekly active accounts for the project |
| a read error code | the last read of an id failed, for example `rate_limited` |

How the platform counts without keeping addresses is on
[On-chain data sources](../reference/onchain-sources.md#weekly-active-accounts).

## Read a project's detail

**Projects**, then a project: the **On-chain** section lists each declared id with its proof, its
daily operations (dashed cells for missing days) and the reason for every missing day, and shows
whether the project's stellar.toml and its accounts' home domain link the verified domain both
ways, and the weekly active accounts of each ISO week of the range. **Compare** adds an
**On-chain** section with each project's daily operations and weekly active accounts over the
range.

## Export what you see

- **Global web** and **Global API**: **Export CSV** under the table downloads exactly the rows the
  table shows, with the same range, filters, sort and order. Load more rows first to export more,
  up to 5,000.
- **Compare**: **Export web CSV** and **Export API CSV** download one row per project and day.

A value that is not known is an empty cell, never 0. Each file carries the on-chain columns, and
each export writes an `export.csv` row to the audit log with the query it answered. List exports
add `onchain_weekly_active_accounts_average`, `onchain_weekly_active_accounts_latest`,
`onchain_weeks_counted` and `onchain_weeks_missing`; compare exports add, on every day row, the ISO
week of the day (`onchain_week`) with its `onchain_week_active_accounts` or
`onchain_week_missing` reason, for the weeks that lie whole inside the range.

## What projects see

Each project has a **What SCF sees** page with its own row of these views, computed by the same
queries for the same range, with its integrity grade and weekly active accounts. It sees nothing
about other projects.

## Machine API and AI clients

- The machine API serves `GET /api/metrics/v1/onchain/ids` (declared ids with proofs and read
  state), `GET /api/metrics/v1/onchain/daily` (the stored daily rows) and
  `GET /api/metrics/v1/onchain/weekly` (weekly active accounts per project and ISO week, with the
  reason for a week without a count), with the key scopes of [API keys](api-keys.md).
- The operator MCP connection has `get_tenant_onchain` with the weekly active accounts; the
  overviews and compares it reads carry the on-chain summary per project, weekly active accounts
  included.

## When reads fail

The jobs task `chain-activity` runs hourly and reads each id once a day. Its metric
`jobs_chain_reads_total{kind, result}` counts reads by result. A burst of `rate_limited` or
`source_unavailable` means the ledger API refused or failed; the next runs catch up, because a day
is stored only once it was read whole. Set `STELLAR_HORIZON_URL` or `STELLAR_RPC_URL` to another
provider to change the source.
