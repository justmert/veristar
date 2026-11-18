---
title: "Alerts"
sidebarTitle: "Alerts"
description: "The console's Alerts page: the platform's live alerts, how to read them, and what the page shows when alerting itself is broken."
---

The **Alerts** tab of the operator area, `https://app.<BASE_DOMAIN>/operator/alerts`,
shows the platform's alerts as Alertmanager holds them right now. It is the platform's alert
channel: operators read firing alerts here. A Discord push channel can be added on top of it
(see [Push notifications](#push-notifications)).

Only operator admins see the tab. The control plane answers its route,
`GET /api/operator/alerts`, with `403 operator_admin_required` for an operator without the
`operator_admin` role and `403 operator_required` for everyone else, and does not ask
Alertmanager for them ([contract section 11](../reference/control-plane-api.md#11-operator-alerts)).
Alert labels and descriptions name nodes and internal addresses, which is why the route
needs the admin role.

## What the page shows

- **Alerts by severity.** `Page` (act now) first, then `Ticket` (act within a working day),
  each with the number of alerts in it. Inside a group the alert that has fired longest
  comes first.
- **Each alert**: its name, which links to its section of the
  [alerts runbook](../runbooks/alerts.md); the summary; the node, service and instance it
  is about, where the alert has them; how long it has been firing and since when (UTC). An
  alert inhibited by another one, such as `VisitorSecretMissing` while `NATSQuorumLost`
  fires, is listed with a note, because it still describes a real condition.
- **All clear**, with the time of the last check, when nothing fires.
- **Last checked**, the time the control plane last asked Alertmanager. The page asks again
  every 30 seconds, also while the browser tab is in the background, and **Check now** asks
  at once.

Silenced alerts are left out: a silence is an operator's decision that the alert needs no
attention for a while. Silences are set in Alertmanager itself (through an SSH tunnel to a
core node, port 9093), not in the console.

Across the operator area:

- The **Alerts** tab carries the number of firing alerts, red while one of severity `page`
  is among them, and `!` while the alert source is unreachable.
- Every operator page of an operator admin shows a banner while an alert of severity `page` fires, with a link to
  the Alerts tab.

Project members never see any of this. The tab, the badge and the banner exist only inside
the operator area, and a member's browser never asks for alerts.

## When alerting itself is broken

The page never shows "all clear" without having asked Alertmanager, and it says so when the
answer cannot be trusted:

| What the page shows | What it means | What to do |
|---|---|---|
| **Alerting pipeline down** | The always-firing `Watchdog` alert is missing. Prometheus is not evaluating the rules or cannot reach Alertmanager, so the list can miss alerts that are due to fire. If nothing else is listed, the page says that this is not all clear. | [Watchdog runbook](../runbooks/alerts.md#watchdog) |
| **Alert source unreachable** | No Alertmanager member answered the control plane. No list is shown. The message names each member and its error. | Check `docker compose ps` in `/opt/veristar/observability` on the core nodes ([alerts runbook](../runbooks/alerts.md#where-alerts-show)) |
| **N of M Alertmanager members did not answer** | Some members are down. The list comes from the others; every member holds every alert, so it is complete. | Bring the named member back |
| **No alert source configured** | The control plane has no `ALERTMANAGER_URLS`. Deployments made with the Ansible roles always set it. | [Contract section 11.3](../reference/control-plane-api.md#113-configuration) |

## Push notifications

Push notifications are optional. When the deployment sets a Discord webhook URL
(`alertmanager_discord_webhook_url` and `gatus_discord_webhook_url` in the environment's
encrypted secrets), Alertmanager also sends every alert group to Discord, and the external
probe's Gatus sends its status page results there
([reference/observability.md](../reference/observability.md)). Adding a receiver is described in
the alerts runbook, [Adding a receiver](../runbooks/alerts.md#adding-a-receiver). The Alerts page
works the same either way.
