---
title: "Deploying Veristar"
sidebarTitle: "Overview"
description: "Stand up, verify, upgrade and remove a Veristar environment from this repository, step by step."
---

How to stand up an environment (staging or production) from this repository, check it, upgrade
it and take it down. Follow the pages in this order:

| Step | Page | What it does |
|---|---|---|
| 1 | [prerequisites.md](prerequisites.md) | accounts, plan inputs I1 to I15, controller tools |
| 2 | [secrets.md](secrets.md) | age identities, SOPS recipients, the two encrypted files per environment |
| 3 | [opentofu.md](opentofu.md) | Azure state storage, VMs, network, Traffic Manager, Cloudflare DNS, backup storage, the Ansible inventory |
| 4 | [ansible.md](ansible.md) | roles, images, firewall, the playbooks |
| 5 | [bootstrap.md](bootstrap.md) | first converge of a new environment, first sign-in, the probe tenant |
| 6 | [verification.md](verification.md) | checks that prove the environment works |
| | [backups.md](backups.md) | the backup storage account (ZRS, versioning, immutability), the stanza and WAL archiving |
| | [probe.md](probe.md) | the prober outside Azure, beside the operator's uptime monitor |
| 7 | [upgrade.md](upgrade.md) | releasing and rolling out a new version |
| 8 | [teardown.md](teardown.md) | removing an environment |

Before production takes its first real project, every item of
the verification page is run and signed off.

[local-converge.md](local-converge.md) is the stand-in that runs the same roles against four to
seven Debian 13 containers on one machine; everything below was first proven there.

## Status on 2026-11-13

Production is being created in Microsoft Azure, region Sweden Central (EU), with DNS at
Cloudflare; there is no separate staging (operator decision of 2026-11-13). Its state storage, network,
addresses, backup account and CAA records exist; the five VMs wait for Azure capacity
([opentofu.md](opentofu.md) section 3.8). PostgreSQL 18.7 is pinned. The first converge also
needs a signed release and an operator input (the verification page). Each page says which
of its steps were run locally and which have never run. Expected outputs of steps that have
never run are taken from the code that prints them (the Makefile, scripts and playbooks), not
from a run, and are marked that way.

Plan inputs are written as `<an operator input>` and so on. Nothing in angle brackets is a real value.
Operator procedures after deployment are in [../runbooks/](../runbooks/overview.md).
