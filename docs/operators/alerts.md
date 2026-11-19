---
title: "Alerts"
sidebarTitle: "Alerts"
description: "The console's Alerts page: the platform's live alerts, how to read them, and what the page shows when alerting itself is broken."
---

The **Alerts** page of the operator area, `https://app.<BASE_DOMAIN>/operator/alerts`,
shows the platform's alerts as Alertmanager holds them right now. It is the platform's alert
channel: operators read firing alerts here. A Discord push channel can be added on top of it
(see [Push notifications](#push-notifications)).

Only operator admins can open the page. The control plane answers its route,
`GET /api/operator/alerts`, with `403 operator_admin_required` for an operator without the
`operator_admin` role and `403 operator_required` for everyone else, and does not ask
Alertmanager for them ([contract section 11](../reference/control-plane-api.md#11-operator-alerts)).
Alert labels and descriptions name nodes and internal addresses, which is why the route
needs the admin role.

## What the page shows

- **Alerts by severity.** **Act now** (severity `page`) first, then **Act within a working day**
  (severity `ticket`), each with the number of alerts in it. Inside a group the alert that has fired longest
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

- The **Alerts** item in the sidebar carries the number of firing alerts, red while one of
  severity `page` is among them, and a red alert icon while the alert source is unreachable.
- Every operator page of an operator admin shows the line "1 critical alert is firing." while an alert of severity
  `page` fires, with the link **Open alerts**.

Project members never see any of this. The page, the badge and the alert line exist only inside
the operator area, and a member's browser never asks for alerts.

## When alerting itself is broken

The page never shows "all clear" without having asked Alertmanager, and it says so when the
answer cannot be trusted:

| What the page shows | What it means | What to do |
|---|---|---|
| **The alert checks do not run** | The always-firing `Watchdog` alert is missing. Prometheus is not evaluating the rules or cannot reach Alertmanager, so the list can miss alerts that are due to fire. If nothing else is listed, the page says that an alert can fire without showing. | [Watchdog runbook](../runbooks/alerts.md#watchdog) |
| **The alert service does not answer** | No Alertmanager member answered the control plane. No list is shown. **Try again** asks at once. | Check `docker compose ps` in `/opt/veristar/observability` on the core nodes ([alerts runbook](../runbooks/alerts.md#where-alerts-show)) |
| **N of M alert servers did not answer** | Some Alertmanager members are down. The list comes from the others; every member holds every alert, so it is complete. **Servers and errors** names each member that did not answer and its error. | Bring the named member back |
| **No alert service is set up** | The control plane has no `ALERTMANAGER_URLS`. Deployments made with the Ansible roles always set it. | [Contract section 11.3](../reference/control-plane-api.md#113-configuration) |

## Push notifications

Push notifications are optional. When the deployment sets a Discord webhook URL
(`alertmanager_discord_webhook_url` and `gatus_discord_webhook_url` in the environment's
encrypted secrets), Alertmanager also sends every alert group to Discord, and the external
probe's Gatus sends its status page results there
([reference/observability.md](../reference/observability.md)). Adding a receiver is described in
the alerts runbook, [Adding a receiver](../runbooks/alerts.md#adding-a-receiver). The Alerts page
works the same either way.
