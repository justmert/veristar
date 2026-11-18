---
title: "Accounts, contracts and the stellar.toml link"
sidebarTitle: "On-chain activity"
description: "Declare your project's Stellar accounts and Soroban contracts, prove you control them, and link your domain and your accounts both ways."
---

Veristar shows the ledger activity of your project's Stellar accounts and Soroban contracts next to
its web and API usage, on your overview, on **SCF review** and in the views SCF reviewers use. You declare the ids,
prove that your project controls each one, and Veristar reads their activity from public ledger
APIs once a day. The contract is section 15 of
[`docs/reference/control-plane-api.md`](../reference/control-plane-api.md).

## What you see

Open your project and choose **On-chain**. The page has three parts: **Activity** for a range
you choose (7, 30, 90 or 365 days, or your own UTC days), **Accounts and contracts**, and the
**stellar.toml link** checklist. **Export CSV** downloads the daily rows of the range.

**Activity** counts your verified ids only, the way SCF reviewers see them: operations,
transactions, weekly active accounts and days without data (days before today without a ledger
read, added up over the verified ids), with the daily operations and every ISO week.

**Accounts and contracts** lists every id you declared, with:

- **Verified** or **Unverified**: whether the project proved control of the id.
- Per UTC day: **operations**, **transactions** and **counterparties** (distinct other accounts or
  contracts). A bar is a day that was read, a flat bar a day read with no activity, and a dashed
  cell a day without data. Every day without data names its reason, such as "not read yet" or "the
  ledger API refused more requests". Veristar never shows a day it could not read as zero.

| Id | Read from | Counted per day |
|---|---|---|
| Account (`G...`) | Horizon | successful operations that involve the account, their transactions, and the other accounts they name |
| Contract (`C...`) | Stellar RPC events | operations and transactions in which the contract emitted an event, and the addresses its event topics name |

The first read of an account covers the last 90 days. A public Stellar RPC server keeps contract
events for about seven days, so a contract's history starts at most a week before you add it, and
a contract that emits no events shows no activity. The [sources page](../reference/onchain-sources.md)
lists every limit.

### Weekly active accounts

The **On-chain** page and **SCF review** show your project's **weekly active
accounts**, by the SCF Growth Hack definition: an active account is "the account that initiated at
least one operation" in the week. Veristar counts the distinct accounts that initiated at least one
successful operation involving your **verified** accounts or contracts in each ISO week (Monday to
Sunday, UTC). An account counts once a week, however many of your ids it used.

Only verified ids count, so prove control of your ids to get a count. A week shows its number once
it is over and every verified id has all seven days read; until then, or when a day could not be
read whole, it shows the reason instead of a number, never zero. The
[sources page](../reference/onchain-sources.md#weekly-active-accounts) explains how each kind of id
is read.

### Calls per contract function

Each verified contract on **Accounts and contracts** shows **Calls per function**: per function,
the calls made by accounts (operations that invoke the contract) and by other contracts, the
failed calls of contracts, and the most distinct accounts behind them on one day. **Daily calls**
shows each function per UTC day, and **Export CSV** downloads those rows. **SCF review** shows
the same totals that reviewers see. Calls are found in the transactions where your contract emits
an event, and only verified contracts count. The
[sources page](../reference/onchain-sources.md#calls-per-contract-function) explains how calls and
failures are read.

## Add an account or a contract

The ids you gave at enrollment are already listed. To add another:

1. Open **On-chain** and enter the id in **Account or contract**.
2. Choose **Add**.

A project declares at most 20 ids. For a muxed account (`M...`), add its `G` account: a muxed
address is a label on that account, and its activity is the account's activity. An owner removes an
id from its actions menu; adding it again brings its history back.

## Prove control of an account

Choose **Prove control** on the account. Veristar creates a challenge that is valid for 30 minutes.
Use either method:

**Sign with a wallet.** Choose **Sign with a wallet**, pick a wallet that holds the account, and
approve the message. The wallet signs it with SEP-53 (Sign and Verify Messages), the standard that
prefixes the message so that a signature can never be a transaction: signing moves no funds and
sends nothing to the ledger. Veristar lists the wallets that sign SEP-53 messages, such as Freighter
5.34 or later, Lobstr and Hana. A signature counts only while the account's own key can still
send a payment on its own: Veristar reads the account after checking the signature, and its master
key weight must be above 0 and at least the account's medium threshold. An account whose master key
was set to weight 0, such as an issuer locked behind several signers, proves control with a data
entry instead.

**Publish a data entry.** Submit a manage-data operation from the account with the entry name and
value the challenge shows (name `veristar`), then choose **Check the data entry**. Any wallet or the
[Stellar Lab](https://lab.stellar.org) builds this operation. It costs the base fee and holds 0.5
XLM of reserve while the entry exists; delete the entry afterwards to release it. Use this method
when your wallet cannot sign messages or the account needs several signers.

A signature or entry proves the account for this project only: the challenge names the console,
the project by name and id, and the account, and works once. Renaming the project while a challenge
is open makes its message stale; create a new challenge after a rename.

## Prove control of a contract

A contract is proven through an account your project has already verified. Choose one:

- **Check the admin.** Veristar reads the contract's instance storage and looks for an address
  stored under `Admin`, `admin`, `Owner` or `owner`, the convention of the Soroban examples, the
  Stellar asset contract and OpenZeppelin's Stellar contracts. The proof holds when that address is
  one of your verified accounts.
- **Check the deployer.** Enter the hash of the transaction that created the contract and choose
  **Check the deployer**. Veristar reads the transaction, recomputes the contract id from the
  deploying address and the salt, and the proof holds when the id matches and the deploying address
  is one of your verified accounts. Horizon keeps about a year of transactions; for an older
  contract, use the admin check. Only a contract the transaction creates itself is read: a
  contract that a factory contract created during a contract call is not, and the check says so;
  prove such a contract with the admin check.

A contract's proof rests on the account it was proven through. Removing that account clears the
proof of every contract proven through it; prove them again through another verified account.

## Link your domain and your accounts both ways

SEP-1 links a domain and its accounts in both directions. Veristar checks both, from your verified
site domain:

1. **Domain to ledger.** Your `https://<domain>/.well-known/stellar.toml` lists the account in
   `ACCOUNTS` and sets `[DOCUMENTATION] ORG_URL` to an https URL on the same domain or its `www.`
   form. Veristar reads the first 50 entries of `ACCOUNTS` and of `[[CURRENCIES]]`, and the file
   must be for the public network: leave out `NETWORK_PASSPHRASE` or set it to the public one.

   ```toml
   ACCOUNTS = ["GDWQ7YV27AVTL25BDOLZD6E44VCQLCEBJXBOOWA2R3VLKCUIZHSXZLDE"]

   [DOCUMENTATION]
   ORG_URL = "https://example.org"
   ```

2. **Ledger to domain.** The account's home domain is your domain. Set it with a SetOptions
   operation (`home_domain = "example.org"`) from the account, in any wallet or the Stellar Lab.
   The ledger keeps at most 32 bytes of home domain, so this leg needs a domain of 32 characters
   or fewer.

The **stellar.toml link** checklist shows each leg per site and account as one line, done or not
done. A line that is not done has one action: verify the domain, open the site setup, or copy the
lines or the domain to set. When every leg holds for a
verified account on a verified domain, the project shows **stellar.toml linked both ways**; an
account you have not proven never links, since anyone can declare an account that lists a domain. Veristar reads
the stellar.toml once a day and the home domain with each daily ledger read, so a change shows
within a day. A contract you list as a `[[CURRENCIES]]` `contract` shows as named by the file.

## SCF review

**SCF review** shows the figures the SCF operator views show about your project for the same
range, from the same queries: your standing (the integrity grade with what it means, your cohort,
and the change and percentile of each key metric), the full integrity report with the
**Appeal this grade** link, the rules you meet, your project's rows in the web and API views, its
on-chain column with the weekly active accounts and the calls per contract function, and their
daily series. The same page creates [signed reports](signed-reports.md) of these figures. **Export CSV** downloads your overview's daily numbers
(web, API and on-chain) for the range. The [overview guide](overview.md) explains each web and API
number.

## What is stored

Ledger data is public. Veristar stores the ids you declare, the proof of each (method and time, and
the transaction hash or storage key a contract proof used), each account's home domain, three
counts per id and day, and one count of weekly active accounts per week. It stores no counterparty
address, no transaction and no signature. While a week is open, the accounts behind its count are
held only as keyed hashes under a key that exists for that week; both are deleted when the week
closes. The
requests to the ledger APIs carry the public ids only.
