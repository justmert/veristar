---
title: "0003: Licensing"
description: "The open-source licence of the repository and of the infrastructure tooling it depends on."
icon: "scale-balanced"
---

- Status: accepted
- Date: 2026-11-12

## Context

The Stellar Community Fund request for proposals (RFP), section 3.1, says: "All application and infrastructure code must be released
under an OSI-approved open-source license", and the whole solution, including
infrastructure as code, must live in one open-source monorepo.

Umami is not forked ([ADR 0002](0002-overlay-not-fork.md)), so its licence does not
dictate the licence of this repository. The one
exception is the browser tracker in `packages/tracker`, whose source is derived
from Umami's tracker. Umami is MIT licensed, with the notice
"Copyright (c) 2022 Umami Software, Inc. \<hello@umami.is\>"
([LICENSE at v3.5.0](https://github.com/umami-software/umami/blob/v3.5.0/LICENSE)).
The MIT licence requires that "The above copyright notice and this permission
notice shall be included in all copies or substantial portions of the Software."

The infrastructure code needs a provisioning tool, and the tool's licence matters
for the same OSI requirement and for a future operator who must rebuild from the
repository. HashiCorp announced on 10 August 2023 that it was "changing its
source code license from Mozilla Public License v2.0 (MPL 2.0) to the Business
Source License (BSL, also known as BUSL) v1.1 on all future releases of HashiCorp
products", which includes Terraform
([HashiCorp announcement](https://www.hashicorp.com/blog/hashicorp-adopts-business-source-license)).
BUSL-1.1 is not on the [OSI list of approved licenses](https://opensource.org/licenses)
and is marked as not OSI approved in the
[SPDX license list](https://spdx.org/licenses/BUSL-1.1.html).
OpenTofu is licensed under MPL-2.0
([LICENSE](https://github.com/opentofu/opentofu/blob/main/LICENSE)), which is
OSI approved.

## Decision

- The repository is licensed under the Apache License 2.0. `LICENSE` holds the
  unmodified text from https://www.apache.org/licenses/LICENSE-2.0.txt. Apache-2.0
  is OSI approved and includes an explicit patent grant.
- `packages/tracker` is licensed under the MIT License and carries its own
  `LICENSE` file with Umami's copyright notice retained, plus a line for the Veristar
  changes. `NOTICE` at the root records this.
- Infrastructure code targets OpenTofu, not Terraform, so that the tool a future
  operator needs is itself open source under an OSI-approved licence.

## Consequences

- Every file in the repository is under an OSI-approved licence (Apache-2.0 or
  MIT), which meets RFP 3.1.
- The tracker can be used, copied and bundled by tenants under the same terms as
  Umami's own tracker.
- Contributors need to know which licence applies where. The root `NOTICE` and
  the tracker's own `LICENSE` make this visible, and new directories default to
  Apache-2.0.
- Terraform-only features or providers that do not publish to the OpenTofu
  registry cannot be used. The providers in use (`hashicorp/azurerm` and
  `cloudflare/cloudflare`) were read from the OpenTofu registry on 2026-11-13.
- Container images and third-party tools the platform runs (Umami, NATS,
  PostgreSQL, Caddy and others) keep their own licences. They are not
  redistributed as part of this repository's source.

## Sources

- RFP section 3.1.
- Apache License 2.0: https://www.apache.org/licenses/LICENSE-2.0.txt
- Umami LICENSE at v3.5.0: https://github.com/umami-software/umami/blob/v3.5.0/LICENSE
- HashiCorp, "HashiCorp adopts Business Source License", 10 August 2023:
  https://www.hashicorp.com/blog/hashicorp-adopts-business-source-license
- OSI approved licenses: https://opensource.org/licenses
- SPDX entry for BUSL-1.1: https://spdx.org/licenses/BUSL-1.1.html
- OpenTofu LICENSE: https://github.com/opentofu/opentofu/blob/main/LICENSE
