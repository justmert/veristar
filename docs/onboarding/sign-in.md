---
title: "Signing in"
description: "Sign in to the console with GitHub or with a Stellar wallet, and link both to one account."
---

You sign in to the console with a GitHub account, with a Stellar wallet, or
with both linked to one account. Each method reaches the same projects.

## Sign in with GitHub

Choose **Continue with GitHub** on the sign-in page. GitHub shares your
numeric user id and your login. The console asks for no scope, so it gets no
email address and no access to your repositories.

## Sign in with a Stellar wallet

1. Install and unlock a Stellar wallet browser extension. The console offers
   Varial (formerly Freighter), xBull, LOBSTR, Hana, Rabet, Klever, OneKey,
   Bitget Wallet, Cactus Link, D'CENT, Fordefi and Scopuly.
2. Choose **Continue with a Stellar wallet** on the sign-in page and pick your
   wallet. Wallets installed in this browser are listed first.
3. Approve the connection in the wallet, then sign the request it shows.

The request is a [SEP-10](https://github.com/stellar/stellar-protocol/blob/master/ecosystem/sep-0010.md)
challenge: a transaction for the public network with sequence number 0 that
names this console. It is never submitted to the network, moves no funds and
costs no fee. The console checks that your account's key signed it, then signs
you in. A challenge expires after five minutes and works once.

The console shares nothing with the wallet beyond the challenge, and stores
only your public address. A wallet-only account appears to other members as
the first and last four characters of the address, such as `GCB6…WOQN`.

Sign with the account's own key. An account whose master key has no weight
(a multisig setup where only other signers can sign) cannot sign in this way.

Wallets that open a web page in a pop-up (Albedo, xBull's web wallet),
connect through WalletConnect, or are hardware wallets (Ledger, Trezor) are
not offered: the console's security headers refuse messages from pop-ups of
other sites, connections to other servers, frames and USB access.

## Link GitHub and a wallet

Open the account menu at the bottom of the sidebar and choose **Account**.
Under **Linked sign-in methods**:

- **Link a wallet** signs a challenge with the wallet you choose and adds its
  address to your account.
- **Link GitHub** runs the GitHub sign-in and adds that GitHub account to your
  account.

You stay signed in while you link, so both proofs come from one session.
A GitHub account or an address that already signs in to another console
account cannot be linked; delete that account, or unlink the method there,
first. An account holds one GitHub account and one address at most.

**Unlink** removes a method. The last method of an account cannot be removed.
Signing in with an unlinked method later creates a new, empty account.

## Being added to a project

A project owner adds you under **Members** by your GitHub username or by your
Stellar address. If you have no account yet, you get an invite and join the
project the first time you sign in with that GitHub account or that wallet.

## What is stored

For GitHub: your numeric GitHub id and login. For a wallet: your Stellar
address. The [records of processing](../compliance/records-of-processing.md)
and the [erasure workflow](../compliance/erasure.md#7-deleting-your-console-account)
describe how long they are kept and how you delete them.
