---
title: "On-chain data sources"
description: "Which public ledger APIs the on-chain witness reads, what each one counts, and their limits."
---

The on-chain witness (contract section 15 of
[control-plane-api.md](control-plane-api.md)) reads public Stellar mainnet APIs only. This page lists
each source, what it gives, and its limits, as checked on 2026-11-16.

## Sources

| Key | Default | Used for |
|---|---|---|
| `STELLAR_HORIZON_URL` | `https://horizon.stellar.org` | account operations, account records (home domain, data entries), transactions of deployer proofs, ledger close times |
| `STELLAR_RPC_URL` | `https://mainnet.sorobanrpc.com` | contract events (`getEvents`), contract instances (`getLedgerEntries`), ledger close times (`getLedgers`), the retention window (`getHealth`) |
| `STELLAR_HORIZON_RPS` | `0.8` | requests per second to Horizon |
| `STELLAR_RPC_RPS` | `2` | requests per second to Stellar RPC |

The control plane (proofs) and the jobs process (daily reads) read the same keys. Every key is
optional; a value that is present is validated at startup.

- **Horizon.** The Stellar Development Foundation runs `horizon.stellar.org` for mainnet. It allows
  3,600 requests per hour per client address and answers 429 above that; it sends no rate limit
  headers, so the client paces itself at 0.8 requests per second. It keeps about one year of
  history (since 1 August 2024 its history is truncated to one year) and answers 410 for older
  ledgers.
- **Stellar RPC.** The Stellar Development Foundation runs no public mainnet RPC server; the
  default is one of the public providers listed in the Stellar developer documentation. Public
  servers keep a window of 120,960 ledgers, about seven days, and serve at most 10,000 events per
  `getEvents` page. Point `STELLAR_RPC_URL` at another provider, a full-history archive server or
  your own node to change the window; the reader works with any server that implements the
  standard methods.

## What each kind of id counts

| Kind | Operations | Transactions | Counterparties |
|---|---|---|---|
| Account | successful operations that involve the account, from `/accounts/{id}/operations` in ledger order | distinct transactions of those operations | distinct other `G` accounts the operations name |
| Contract | distinct (transaction, operation) pairs with an event the contract emitted in a successful call | distinct transactions with such an event | distinct `G` and `C` addresses in the event topics, other than the contract |

No public API gives per-contract daily invocation counts over full history: Horizon has no
per-contract endpoint, and full history needs an indexer (the Hubble dataset on BigQuery, which the
querier pays for, or a full-archive RPC server scanned in full). The witness therefore reads
contract events from the RPC window and stores each day once it is complete, so a contract's
history grows from the day it is declared. A contract that emits no events shows no activity.

## How a day is read

- A day is a UTC day and is read once it is over. The reader finds the first ledger of the day by
  a binary search over ledger close times, then pages forward from it.
- A day is stored only when it was read whole. An id with more than 20,000 operations (accounts)
  or 50,000 events (contracts) to read in one run stores the days it completed and records
  `day_over_read_cap` for the rest. Busy contracts, such as a stablecoin's asset contract, reach
  this cap; a project's own contracts and accounts rarely do.
- Requests are retried on 429 and 5xx with exponential backoff and jitter, never shorter than a
  `Retry-After`. After six attempts the id records `rate_limited` or `source_unavailable`, and the
  next hourly run retries it.
- The RPC reader starts 100 ledgers (about ten minutes) above the server's oldest ledger, because
  the server drops its oldest ledgers continuously.

## Live check

`internal/chain/live_test.go` reads the default sources with the production code and runs only
with `-tags live`:

```sh
go test -tags live -count=1 -v -run TestLiveSources ./internal/chain/
```

The run of 2026-11-16 (UTC) recorded:

| Check | Result |
|---|---|
| Horizon range | ledgers 59,204,881 to 65,512,226; first whole day 2025-10-04 |
| Account record of Circle's USDC issuer `GA5Z...KZVN` | home domain `circle.com` |
| RPC window at `mainnet.sorobanrpc.com` | ledgers 65,391,368 to 65,512,227, `ledgerRetentionWindow` 120,960; first whole day 2026-11-10 |
| USDC asset contract `CCW6...MI75`, 2026-11-15 | `day_over_read_cap`: more than 50,000 events in the day, so the day is left out |
| A quiet contract `CDPK...Q5ZT` found among recent events, 2026-11-15 | 23,897 operations, 8,422 transactions, 47 counterparties |
| USDC issuer account, 2026-11-15 | 3 operations, 3 transactions, 1 counterparty |
| Admin of the USDC asset contract | key `Admin`, address `CCPL...R4D5W` |

An earlier run of the same day found that a server can drop its oldest ledger between `getHealth`
and the next request; the reader's 100-ledger margin above comes from that run.
