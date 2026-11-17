---
title: "Adding and removing operators"
description: "Manage the operator roster, keep the deploy settings as the bootstrap, and recover from a lockout."
---

Operators are the SCF people who review projects, suspend or remove them, read the global views
and manage operator API keys. The operator roster is the table `cp.operators`; operator admins
manage it on the console's **Operators** page ([Operators and passkeys](../operators/roster.md)).
`cp.users.is_operator` follows the roster in the database, so a change takes effect at the
person's next request (section 5.1 of the
[control plane API reference](../reference/control-plane-api.md)). Operators get no Umami access
and are not members of any project. Every operator confirms the operator area with a passkey
(section 5.15).

## The deploy settings are the bootstrap

The Ansible variables `sa_operator_github_ids` (comma-separated numeric GitHub ids, required) and
`sa_operator_stellar_addresses` in `infra/ansible/inventories/<env>/group_vars/all.yml` become
`OPERATOR_GITHUB_IDS` and `OPERATOR_STELLAR_ADDRESSES`. The second is a comma-separated string,
empty when none: a wallet operator is listed by account id (G-address), a passkey operator by the
passkey account address (C-address) that the console shows on their **Account** page. At every
start the control plane:

1. makes each listed identity an active operator admin of the roster (audit `operator.bootstrap`,
   actor `system:startup`, written only when a row was inserted or changed);
2. opens a first-passkey window of `OPERATOR_ENROLL_WINDOW` (default 24 hours) for each listed
   identity whose account holds no passkey;
3. recomputes every account's operator flag (audit `user.operator_sync` for each change);
4. refuses to start when the roster has no active operator admin.

The settings only add. An identity taken out of them keeps its row until an operator admin removes
it on the Operators page. `bootstrap.yml` refuses to run without `sa_operator_github_ids` or with
anything other than digits and commas; the core role refuses anything other than G-addresses,
C-addresses and commas in `sa_operator_stellar_addresses`, and the control plane checks each
checksum at start.

Keep the settings to the people who are the path back in after a lockout: SCF's first operator
admin, and one person of the hosting party if SCF agrees. Every other operator is added in the
console.

## Add an operator

An operator admin adds the person on the Operators page with their GitHub login or Stellar address
([Operators and passkeys](../operators/roster.md), "Add an operator"). Nothing is deployed. The
person signs in and registers a passkey within 24 hours.

To add someone through the settings instead, for the first operator admin of a new environment:

1. Look up the numeric id of the GitHub account. The login can change; the id cannot.

   ```sh
   curl -s https://api.github.com/users/<github login> | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])'
   ```

   Confirm the id with the person through a channel other than the request itself.
2. Add the id to `sa_operator_github_ids`, for example `sa_operator_github_ids: "<id 1>,<id 2>"`, and
   commit it with a line in the change description saying who and why.
3. Converge the core nodes:

   ```sh
   make <env>-deploy SA_IMAGE_TAG=<current release> SA_RELEASE_MANIFEST=<release-images.json>
   ```

   `<env>` is `staging` or `production`. The core compose file changes, so `controlplane` is
   recreated on every core node and bootstraps the roster at start.
4. Within 24 hours of the start, the person signs in to `https://app.<BASE_DOMAIN>/`, opens the
   operator area and registers a passkey.

## Remove an operator

1. If the identity is in `sa_operator_github_ids` or `sa_operator_stellar_addresses`, take it out,
   commit, and converge as above. The row stays; the console can now change it.
2. An operator admin selects **Remove** (or **Suspend**, to keep the passkeys for a later return) on
   the person's row and gives the reason. The person's next request is refused, their console
   sessions end, their operator MCP credentials are revoked, and a removal deletes their operator
   passkeys unless another active row still makes the person an operator.
3. Revoke the operator API keys that person created, unless another operator takes them over
   knowingly ([operator-key-revocation.md](operator-key-revocation.md)). To list them, on the
   database leader:

   ```sql
   select k.id, k.name, k.prefix, k.created_at, k.expires_at, k.revoked_at
   from cp.operator_api_keys k join cp.users u on u.id = k.created_by
   where u.github_id = <github id>;
   ```

4. If the removal is because the account may be compromised, follow
   [security-incident.md](security-incident.md) as well.

The removed person keeps an ordinary console account: they can still sign in and use projects
they are a member of. Their earlier operator actions stay in the audit log.

## Lockout recovery

**An operator lost every passkey.** An operator admin selects **Reset passkeys** on the person's
row. The passkeys they added for the operator area are deleted, their sessions end and a new 24-hour
window opens. The person signs in again and registers a new passkey. A sign-in passkey is one of the
account's identities and stays; if it is the lost one, the person unlinks it on the **Account** page
after signing in another way.

**No operator admin can open the operator area** (every admin lost their passkeys, left, or is
unreachable):

1. Put the GitHub id of the person who takes over in `sa_operator_github_ids`, or keep the one that
   is there, and converge as in "Add an operator". The start makes the identity an active operator
   admin again, even if it was suspended or demoted.
2. If that person's account still holds passkeys they no longer have, delete the ones added for
   the operator area on the database leader (a lost sign-in passkey is unlinked on the **Account**
   page once the person signs in with GitHub or a wallet). Connect as the `postgres` superuser, as in
   [Which core node holds the database](overview.md#which-core-node-holds-the-database):
   `cp.users`, `cp.operator_passkeys` and `cp.sessions` enforce row-level security for every other
   role, so the `platform` database owner sees no row and deletes nothing.

   ```sh
   cd /opt/veristar/core && sudo docker compose exec postgres psql -U postgres -d platform
   ```

   ```sql
   begin;
   select id from cp.users where github_id = <github id> and deleted_at is null;
   delete from cp.operator_passkeys
   where user_id = (select id from cp.users where github_id = <github id> and deleted_at is null);
   delete from cp.sessions
   where user_id = (select id from cp.users where github_id = <github id> and deleted_at is null);
   ```

   The `select` returns exactly one row and the first `delete` reports `DELETE 1` or more (one
   per passkey); the second reports the sessions it ended. Run `commit;` when they do. On `(0 rows)`
   or a first `DELETE 0`, run `rollback;` and check the GitHub id: no live account with passkeys
   holds it.

   Restart the control plane on one core node (`docker compose restart controlplane`). With no
   passkey left, the start opens a new first-passkey window for the identity.
3. The person signs in within `OPERATOR_ENROLL_WINDOW` and registers a passkey. The audit log holds
   `operator.bootstrap` and `operator.passkey_add` for the recovery.

**A change by hand in the database would leave no active operator admin.** The constraint trigger
`operators_last_admin` refuses it. Add or reactivate another admin in the same transaction.

## Exercised

| Date | What ran | Result | Source |
|---|---|---|---|
| 2026-11-12 | Local converge with `sa_operator_github_ids: "1001"` (a test-only fixture user) | the fixture operator signed in and created a tenant through the console API | [../deploy/local-converge.md](../deploy/local-converge.md) section 3 |
| 2026-11-14 | Production: an operator listed in `sa_operator_github_ids` signed in to `https://app.veristar.app` | `cp.users.is_operator` true, audit row `user.sign_in` | [../reference/drills/2026-11-13-azure.md](../reference/drills/2026-11-13-azure.md), "End to end" |
| 2026-11-16 | `go test -race -run 'TestBootstrap\|TestRoster' ./internal/controlplane/operator/` and `TestStepUp` (`./internal/controlplane/stepup/`) against real PostgreSQL: the bootstrap, the flag derived from the roster, the last-admin constraint, the roster routes, a suspension ending sessions, a passkey reset, and the step-up ceremonies with a software authenticator | all pass | this page |
