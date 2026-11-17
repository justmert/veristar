---
title: "Operators and passkeys"
description: "Manage the operator roster in the console and confirm the operator area with a passkey."
---

The operator roster lists the people who hold the operator view. Operator admins manage it on the
**Operators** page (Admin, Operators). Every operator confirms the operator area with a passkey or a
security key on top of the sign-in.

## Roles

| Role | Can |
|---|---|
| Operator | Everything in the operator area except changing the roster |
| Operator admin | The same, and add, suspend, resume, promote, demote and remove operators, and reset their passkeys |

Each roster row names one identity: a GitHub account, or a Stellar address, which is a wallet's
`G...` address or the `C...` address of a passkey account. A person who signs in several ways can
hold one row per identity.

## Rows from the deploy settings

The GitHub ids in `OPERATOR_GITHUB_IDS` and the addresses in `OPERATOR_STELLAR_ADDRESSES` are
operator admins at every start of the control plane. The Operators page marks their rows
**Deploy settings**. You cannot suspend, remove or demote these rows in the console, because the
next start would undo the change; take the identity out of the settings first
([Adding and removing operators](../runbooks/operators.md)). The settings only add: an identity
taken out of them keeps its row until an operator admin removes it in the console.

Once SCF's own operator admins are on the roster, keep the settings to the one or two identities
that are your path back in (see "Lockout recovery" below).

## Add an operator

1. On the Operators page, under **Add an operator**, pick **GitHub** or **Stellar wallet or
   passkey**.
2. Enter the GitHub login, or the Stellar address: a wallet's `G...` address, or the `C...` address
   of a passkey account, which the person finds on their **Account** page. A GitHub login is resolved to the account's
   numeric id at once, so a later rename of the login does not move the access.
3. Pick the role, enter a reason, and select **Add operator**.

The new operator signs in, opens the operator area and registers a passkey. The first passkey needs
no confirmation if it is registered within 24 hours of being added (`OPERATOR_ENROLL_WINDOW`), from a
sign-in that happened after the row was added. After the window, an operator admin resets the
person's passkeys, which opens a new window.

## Suspend, resume, change the role, remove

Each action asks for a reason. The reason, the actor and the change go to the audit log
(`operator.suspend`, `operator.resume`, `operator.role_change`, `operator.remove`). Adding an
operator writes `operator.add`, and a passkey reset writes `operator.passkeys_reset`.

- **Suspend** ends the operator view at once: the person's next request is refused, their console
  sessions end, and their AI assistant connections with operator access are revoked. Their passkeys
  stay, so a resumption needs only a sign-in and a confirmation.
- **Remove** takes the row off the roster and the person's sessions end. Their operator passkeys are
  deleted unless another active row still makes them an operator (a second identity of the same
  person).
  They keep their ordinary console account and their project memberships.
- **Make admin** and **Make operator** change the role.

The roster always keeps at least one active operator admin. A suspension, removal or demotion of the
last one is refused with "The roster needs at least one active operator admin", and so is the
account deletion of the last one.

## Passkeys

The operator area asks for a passkey when you open it, and again after
`OPERATOR_STEP_UP_MAX_AGE` (12 hours by default) in the same session. The console uses the same
passkeys as passkey sign-in: a passkey on your phone or computer, or a security key with a PIN. The
browser shows its own prompt; confirm with your fingerprint, face, screen lock or the key's PIN.

- **If you sign in with a passkey**, that passkey also confirms the operator area. Select **Use
  your passkey**, then **Confirm with your passkey**.
- **Your first passkey**: if your account has none, the operator area shows **Register a passkey**
  while your enrollment window is open. Give it a name, select **Register**, then **Create a
  passkey** and **Confirm with your passkey**. The registration also confirms the session.
- **More passkeys**: on the Operators page, under **Your passkeys**, enter a name and select **Add a
  passkey**. Keep at least two, for example your phone and a security key, so a lost one does not
  lock you out.
- **Delete a passkey**: select **Delete** next to it. The account always keeps one. Your sign-in
  passkey is listed but managed on the **Account** page.
- A passkey is bound to the console's address. It does not work on another site, so a look-alike
  page cannot use it.

## What needs the confirmation

Every page and request of the operator area, the CSV exports, and approving an AI assistant
connection with operator access. The machine metrics API and an AI assistant connection that already
holds operator access do not: they authenticate with their own key, which an operator creates in the
confirmed area, and which an operator revokes there.

## Lockout recovery

| Situation | Recovery |
|---|---|
| An operator lost every passkey | An operator admin selects **Reset passkeys** on their row; the passkeys they added are deleted and the person registers a new one within the window. A lost sign-in passkey is unlinked on the Account page from another sign-in method |
| Every operator admin lost their passkeys, or no admin is reachable | The hosting party puts an identity in `OPERATOR_GITHUB_IDS` and restarts the control plane; see the runbook |
| The last admin is suspended or removed in the database by hand | The database refuses the change: the constraint trigger `operators_last_admin` keeps one active admin |

The runbook [Adding and removing operators](../runbooks/operators.md) has the steps.
