---
title: "Tearing down an environment"
sidebarTitle: "Teardown"
description: "Remove an environment's servers, network, addresses and DNS, with protection for production."
---

Removes an environment's VMs, network, addresses, Traffic Manager profiles and DNS records.
Staging is meant to be rebuilt (this stage drills, this stage rehearsal); production carries delete
locks so it cannot be destroyed by one command.

**Status:** never run against a cloud account. The local converge has its own teardown,
`make local-converge-down`, which ran on 2026-11-13 after the this stage drills
([local-converge.md](local-converge.md) section 2).

## Before you start

- **Data.** Destroying the servers destroys both databases (Umami and platform), the NATS
  streams with anything still queued, and every Caddy certificate. The destroy targets keep the
  backup storage account, the core identity and the environment's resource group
  (`-exclude`, see the Makefile), so backups survive, encrypted with `pgbackrest_cipher_pass`
  from the SOPS file. Keep that file and at least one recipient's identity as long as you keep
  backups. The OpenTofu state account is never touched by a destroy.
- **Queues.** If tenants send data, drain first: stop sending is not in your hands, so announce
  the shutdown, wait until `WEB_EVENTS` and `API_METRICS` are empty
  ([../runbooks/queue-backlog-and-dlq.md](../runbooks/queue-backlog-and-dlq.md)), and take a last
  backup:

  ```sh
  cd /opt/veristar/core && sudo docker compose exec postgres pgbackrest --stanza=sa --type=full backup
  ```

## Staging

1. Destroy:

   ```sh
   make staging-destroy
   ```

   OpenTofu lists what it will destroy and asks for `yes`. Expected:
   `Destroy complete! Resources: <n> destroyed.`

2. Remove the generated inventory, which now points at nothing:
   `rm infra/ansible/inventories/staging/hosts.json`.

3. What stays, on purpose: the state account, the backup storage account with its identity and
   resource group, the SOPS files, `terraform.tfvars` and `group_vars/all.yml`. A later
   `make staging-plan` and `make staging-apply` rebuild the same environment
   ([bootstrap.md](bootstrap.md)). To remove the backups as well, once they are no longer
   wanted: `make staging-destroy TEARDOWN_ALL=1`. Azure refuses to delete the storage account
   while a version is still within its immutability period (7 days after it was written), so
   run it when no backup was written for 7 days, or after deleting the blobs whose period ended.

## Production

1. `make production-destroy` refuses unless `CONFIRM=production` is set
   (`set CONFIRM=production to destroy production`), and even then Azure refuses while the
   delete locks exist.
2. To really remove production: set `protect` to false in `infra/tofu/envs/production/env.tf`,
   review it, `make production-plan` and `make production-apply` (expected: only the locks are
   removed), then:

   ```sh
   make production-destroy CONFIRM=production
   ```

3. Then as staging steps 2 and 3. For a handover rather than a shutdown, follow the handover plan
   instead (`docs/handover/`): the repository, the domain and the data move, nothing is destroyed.

## Accounts

After the last environment is gone, revoke what the operator inputs granted: remove the operators'
role assignments on the Azure subscription and, if nothing else uses it, cancel it; delete
the Cloudflare API tokens, the Discord webhook,
the GitHub OAuth Apps, and remove the age recipients only once nothing encrypted to them
is still needed.
