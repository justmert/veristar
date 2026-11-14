---
title: "Security incident"
description: "Respond to a suspected attack or breach, with clear roles and steps."
---

Something suggests the platform's confidentiality, integrity or availability was attacked or
broken: a credential seen where it should not be, a node behaving strangely, a tenant seeing
another tenant's data, a tracker file that does not match its hash, an alert nobody can explain.
For a vulnerability report or advisory without signs of exploitation, use
[security-patch.md](security-patch.md) instead. The threats and what is built against each are
in [../security/threat-model.md](../security/threat-model.md); this page is what to do.

## Roles

One person leads (the first operator on the scene until handed over) and keeps a written
timeline in UTC: what was seen, what was done, by whom. Nothing is deleted or rebuilt before its
evidence is saved (step 3), unless the damage is ongoing.

## Steps

1. **Open a private record.** A GitHub security advisory draft on the repository
   (`github.com/justmert/veristar`, Security, Advisories, New draft) holds the timeline and stays private.
   Do not discuss details in public issues.

2. **Decide what is affected**, using the threat model's sections as the checklist:

   | Sign | Section | Likely blast radius |
   |---|---|---|
   | `sdk.js` served with a different hash, or a release file changed | 3.1 tracker compromise | every tenant page loading that version |
   | A tenant sees another tenant's data | 3.2 cross-tenant leak | the tenants involved |
   | An OTLP token, operator key, session or SOPS value outside where it belongs | 3.5 token theft, 3.8 operator credential compromise | what that credential can read or do |
   | Unknown processes, logins or files on a node | 3.9 edge node compromise (core nodes likewise, with database credentials) | the node, its secrets, the day's visitor tokens |
   | Visitor addresses or user agents in a log, table or queue | 3.11 personal data in storage | the visitors concerned |

3. **Save evidence** before changing anything on an affected node:

   ```sh
   cd /opt/veristar/<edge or core>
   sudo docker compose ps > /tmp/incident-ps.txt
   sudo sh -c 'docker compose logs --timestamps --no-color > /root/incident-compose.log'
   sudo journalctl --since '<YYYY-MM-DD HH:MM>' --utc > /tmp/incident-journal.txt
   sudo last -F > /tmp/incident-logins.txt
   ```

   Copy the files to the controller with `scp`. Docker keeps container logs with the `local`
   driver, which rotates; save them early. For the database, the audit log is append-only for the
   application roles:

   ```sql
   select at, actor, action, target, detail from cp.audit_log where at >= '<YYYY-MM-DD>' order by at;
   ```

4. **Contain**, with the narrowest action that stops the harm. Ingestion keeps running in every
   case below unless stated, because the edge does not need the core tier.

   | Situation | Containment |
   |---|---|
   | Tracker file in question | revoke the version ([tracker-release.md](tracker-release.md)) |
   | Leak through the dashboards or the control plane | stop `umami` and/or `controlplane` on every core node (`sudo docker compose stop umami controlplane`); events queue for 72 hours; put a notice on the status page |
   | Route-level flaw | block the route in `deploy/caddy/web.Caddyfile` or `app.Caddyfile` ([security-patch.md](security-patch.md) section 3) |
   | OTLP token leaked | the tenant revokes it on its token page; an operator can suspend the tenant ([tenant-removal.md](tenant-removal.md)) |
   | Operator key leaked | revoke it ([operator-key-revocation.md](operator-key-revocation.md)) |
   | Operator GitHub account taken over | remove the id from `OPERATOR_GITHUB_IDS` and redeploy, end every console session (below), revoke keys that person created ([operators.md](operators.md)) |
   | Node compromised | drain it ([edge-node-failure.md](edge-node-failure.md) step 2), then stop its services; replace the server rather than clean it; rotate every secret the node held ([secret-rotation.md](secret-rotation.md)) |
   | SOPS file or an age identity exposed | rotate every value in that environment's file ([secret-rotation.md](secret-rotation.md)) |

   End every console session at once (every user signs in again), on the database leader:

   ```sql
   delete from cp.sessions;
   ```

   End every Umami session at once: rotate `umami_user_password_key`
   ([secret-rotation.md](secret-rotation.md)); a changed password invalidates Umami's tokens.

5. **Eradicate and recover.** Fix the cause (patch per [security-patch.md](security-patch.md)),
   rebuild compromised servers from OpenTofu and Ansible, restore data if it was changed
   ([point-in-time-restore.md](point-in-time-restore.md)), and bring services back.

6. **Notify.** The platform processes visitor data for its tenants under the DPA template
   ([../compliance/dpa-template.md](../compliance/dpa-template.md)): it notifies affected tenants
   without undue delay after becoming aware of a personal data breach affecting their data, with
   what the tenant needs for its own obligations. Tell SCF.

7. **Close.** Publish the advisory once fixed, with credit to a reporter who wants it
   ([../security/SECURITY.md](../security/SECURITY.md)). Write the timeline, the cause and what
   changes into the advisory, and update the threat model if a threat was missing.

## Exercised

The steps this runbook relies on are exercised in their own runbooks:

| Date | What ran | Source |
|---|---|---|
| 2026-11-13 | the patch path, signal to all gates green | [security-patch.md](security-patch.md) section 7 |
| 2026-11-13 | tracker version revocation on a local gateway | [tracker-release.md](tracker-release.md) |
| 2026-11-13 | operator key revocation through `TestMachineAPI` | [operator-key-revocation.md](operator-key-revocation.md) |
| 2026-11-13 | rotation of the platform role passwords | [secret-rotation.md](secret-rotation.md) |
| 2026-11-13 | a node drained, powered off and replaced in DNS on production | [edge-node-failure.md](edge-node-failure.md) |
