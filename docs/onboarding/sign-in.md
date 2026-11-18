---
title: "Signing in"
description: "Sign in to the console with GitHub, a Stellar wallet or a passkey, and link them to one account."
---

You sign in to the console with a GitHub account, a Stellar wallet or a
passkey, or with several of them linked to one account. Each method reaches
the same projects.

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

## Sign in with a passkey

A passkey signs you in with Face ID, Touch ID, Windows Hello, the screen lock
of an Android phone or a security key, with no password. The console uses
[soropass](https://docs.soropass.dev/docs) passkeys: the passkey's key is also
the signer of a Stellar smart account (a contract account, `C…` address), and
that address names your console account.

**Create an account with a new passkey:**

1. Choose **Continue with a passkey** on the sign-in page, then
   **Create a new passkey**.
2. Confirm with your device. The browser saves the passkey for this console.
3. Choose **Confirm with your passkey** and confirm with your device once more.
   This second step proves that you hold the new passkey; the console signs
   you in after it.

**Sign in again:** choose **Continue with a passkey**, then
**Sign in with your passkey**, and pick the passkey your browser offers. A
passkey that your password manager syncs (iCloud Keychain, Google Password
Manager, 1Password and others) signs in from each of your devices.

The console checks that the passkey signed a fresh challenge for this console's
address, with your PIN or biometric check, and that the challenge was issued to
this browser. A challenge expires after five minutes and works once. Your
fingerprint or face never leaves your device; the console receives only the
passkey's public key, its credential id and the signature.

The console computes the address of your smart account from the passkey's
credential id and key, the way the soropass account factory on the public
network deploys it, so the address belongs to your passkey's key and no other.
The console uses the address as your identity only: it does not deploy the
account, sign transactions with it or send anything to the network. A
passkey-only account appears to other members as the first and last four
characters of the address, such as `CBH2…AXAO`.

A passkey belongs to the site it was created on. A passkey made on another
site, including another soropass application, does not appear here; create one
for this console.

The console accepts passkeys that sign with ES256 (P-256) and verify you with
a PIN or biometric check, which current browsers and devices do. Use a current
version of Chrome, Edge, Firefox or Safari.

## Link GitHub, a wallet and a passkey

Open the account menu at the bottom of the sidebar and choose **Account**.
Under **Sign-in methods**:

- **Link a wallet** signs a challenge with the wallet you choose and adds its
  address to your account.
- **Link GitHub** runs the GitHub sign-in and adds that GitHub account to your
  account.
- **Add a passkey** creates a new passkey for your account and asks you to
  confirm it once.

You stay signed in while you link, so both proofs come from one session.
A GitHub account, an address or a passkey that already signs in to another
console account cannot be linked; delete that account, or unlink the method
there, first. An account holds one GitHub account, one wallet address and one
sign-in passkey at most; an operator adds more passkeys for the operator area
([roster.md](../operators/roster.md)).

**Unlink** removes a method. The last method of an account cannot be removed.
Signing in with an unlinked GitHub account or wallet later creates a new,
empty account. An unlinked passkey no longer signs in; delete it from your
device, and add a new passkey to sign in with one again.

## Being added to a project

A project owner adds you under **Members** by your GitHub username, by your
wallet's Stellar address (`G…`) or by your passkey account address (`C…`). If
you have no account yet, you get an invite and join the project the first time
you sign in with that GitHub account, wallet or passkey.

## What is stored

For GitHub: your numeric GitHub id and login. For a wallet: your Stellar
address. For a passkey: its credential id, its public key and your smart
account address. The [records of processing](../compliance/records-of-processing.md)
and the [erasure workflow](../compliance/erasure.md#7-deleting-your-console-account)
describe how long they are kept and how you delete them.
