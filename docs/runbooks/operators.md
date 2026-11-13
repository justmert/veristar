# Adding and removing operators

Operators are the SCF people who review projects, suspend or remove them, read the global views
and manage operator API keys. An operator is a GitHub account whose **numeric** id is listed in
`OPERATOR_GITHUB_IDS` (`<an operator input>`). There is no operator role in the database to grant by hand:
the control plane sets `cp.users.is_operator` from that list at every sign-in and once at every
start, and every operator route also checks the id against the loaded list
([../reference/control-plane-api.md](../reference/control-plane-api.md) section 5.1). Operators
get no Umami access and are not members of any project.

The list is the Ansible variable `sa_operator_github_ids`, a comma-separated string of numeric
ids, set per environment in `infra/ansible/inventories/<env>/group_vars/all.yml`. `bootstrap.yml`
refuses to run without it or with anything other than digits and commas.

## Find the numeric id

The login can change; the id cannot. Look it up from the login the person gives you:

```sh
curl -s https://api.github.com/users/<github login> | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])'
```

Confirm the id with the person through a channel other than the request itself.

## Add an operator

1. Add the id to `sa_operator_github_ids` in `infra/ansible/inventories/<env>/group_vars/all.yml`,
   for example `sa_operator_github_ids: "<id 1>,<id 2>"`, and commit it with a line in the
   change description saying who and why.
2. Converge the core nodes so the control plane gets the new list:

   ```sh
   make staging-deploy SA_IMAGE_TAG=<current release>
   ```

   The core compose file changes (`OPERATOR_GITHUB_IDS`), so `controlplane` is recreated on both
   core nodes. At start it syncs the flag for every user; each changed user gets an audit row
   `user.operator_sync` (actor `system:startup`).
3. The person signs in to `https://app.<an operator input>/`. The operator area appears in the console.
   If they had never signed in before, their user row is created at that sign-in with the flag set.

## Remove an operator

1. Remove the id from `sa_operator_github_ids`, commit, and converge as above.
2. When the control plane restarts, it sets `is_operator = false` for that user and writes
   `user.operator_sync`. From then on every operator route answers `403 operator_required` for
   them, even with a session that is still open.
3. Revoke the operator API keys that person created, unless another operator takes them over
   knowingly ([operator-key-revocation.md](operator-key-revocation.md)). To list them, on the
   database leader:

   ```sql
   select k.id, k.name, k.prefix, k.created_at, k.expires_at, k.revoked_at
   from cp.operator_api_keys k join cp.users u on u.id = k.created_by
   where u.github_id = <github id>;
   ```

4. If the removal is because the account may be compromised, also end that person's console
   sessions at once:

   ```sql
   delete from cp.sessions where user_id = (select id from cp.users where github_id = <github id>);
   ```

   and follow [security-incident.md](security-incident.md).

The removed person keeps an ordinary console account: they can still sign in and use projects
they are a member of. Their earlier operator actions stay in the audit log.

## Exercised

| Date | What ran | Result | Source |
|---|---|---|---|
| this stage | Operator lifecycle suite, console e2e | operator lifecycle 6/6 | plan status, this stage |
| 2026-11-13 | this stage: `go test -race -run 'TestSyncFlags\|TestOperatorFlag' ./internal/controlplane/operator/ ./internal/controlplane/auth/` and `TestLifecycle/OperatorIDRemoved` (real PostgreSQL; the start-up sync and the sign-in flag, and an operator whose id was removed getting 403) | all pass | this page |
| 2026-11-12 | Local converge with `sa_operator_github_ids: "1001"` (test-only fixture user) | the fixture operator signed in and created a tenant through the console API | [../deploy/local-converge.md](../deploy/local-converge.md) section 3 |

Not yet exercised with real GitHub accounts an operator input for the OAuth app, I3 for the ids) or on a
deployed environment.
