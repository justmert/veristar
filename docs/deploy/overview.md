---
title: "Deploying Veristar"
sidebarTitle: "Overview"
description: "Stand up, verify, upgrade and remove a Veristar environment from this repository, step by step."
---

This section shows you how to stand up a Veristar environment from this repository, check that it
works, upgrade it and take it down. The repository defines two cloud environments, `staging` and
`production` (`infra/tofu/envs/` and `infra/ansible/inventories/`). Every command takes the
environment name, so the steps are the same for both.

## The reference deployment

Production runs in Microsoft Azure, region Denmark East, on three converged nodes, one in each
of availability zones 1, 2 and 3. Each node runs the ingestion (edge) and the dashboard (core)
roles. Azure Traffic Manager answers DNS for the public names with the addresses of the healthy
nodes, and Cloudflare holds the zone. The measured failover times are in
[../reference/drills/2026-11-13-azure.md](../reference/drills/2026-11-13-azure.md), and the
availability design is in [../reference/availability.md](../reference/availability.md).

## Pages in order

| Step | Page | What it does |
|---|---|---|
| 1 | [prerequisites.md](prerequisites.md) | the accounts and values you provide, the controller tools |
| 2 | [secrets.md](secrets.md) | age identities, SOPS recipients, the encrypted files of each environment |
| 3 | [opentofu.md](opentofu.md) | Azure state storage, VMs, network, Traffic Manager, Cloudflare DNS, backup storage, the Ansible inventory |
| 4 | [ansible.md](ansible.md) | roles, images, firewall, the playbooks |
| 5 | [bootstrap.md](bootstrap.md) | first converge of a new environment, first sign-in, the probe tenant |
| 6 | [verification.md](verification.md) | checks that prove the environment works |
| 7 | [upgrade.md](upgrade.md) | releasing and rolling out a new version |
| 8 | [teardown.md](teardown.md) | removing an environment |

Related pages:

- [backups.md](backups.md): the backup storage account (zone-redundant, versioned, immutable),
  the pgBackRest stanza and WAL archiving.
- [probe.md](probe.md): the external prober and the public status page, which run on a separate
  virtual machine in Azure East US.
- the verification page: the checks an environment passes before
  it takes its first real project.
- [local-converge.md](local-converge.md): the same Ansible roles against Debian 13 containers on
  one machine, for testing role changes without a cloud account.

## Conventions

Values you provide are written in angle brackets, for example `<BASE_DOMAIN>` or
`<subscription id>`. Nothing in angle brackets is a real value. The examples use `staging`;
replace it with `production` for production.

Procedures for running the service after deployment are in the
[runbooks](../runbooks/overview.md).
