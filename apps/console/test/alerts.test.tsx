import { act } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ALERTS_REFRESH_MS, operatorAlertsQuery } from "@/api/queries";
import { operatorAlerts } from "@/api/schemas";
import { criticalCount, firingFor, groupBySeverity, runbookUrl } from "@/lib/alerts";
import { AlertsPage } from "@/pages/operator/alerts";
import { OperatorLayout } from "@/pages/operator/layout";
import * as f from "./fixtures/contract";
import { byRole, click, renderRoutes, stubFetch, waitFor } from "./helpers";

const CHECKED = "2026-11-13T12:00:05Z";

// contract 11.1 bodies
function alert(over: Record<string, unknown> = {}) {
  return {
    fingerprint: "f-down",
    name: "ServiceDown",
    severity: "page",
    summary: "controlplane on core-1 does not answer scrapes",
    description: null,
    node: "core-1",
    instance: null,
    service: "controlplane",
    starts_at: "2026-11-13T09:55:00Z",
    state: "active",
    inhibited_by: [],
    runbook: "docs/runbooks/alerts.md#servicedown",
    labels: { alertname: "ServiceDown", severity: "page", node: "core-1", job: "controlplane" },
    ...over,
  };
}

const source = { address: "10.0.1.4:9093", reachable: true, watchdog_firing: true, error: null };

function body(over: Record<string, unknown> = {}) {
  return {
    checked_at: CHECKED,
    watchdog_firing: true,
    sources: [source],
    alerts: [] as unknown[],
    ...over,
  };
}

const ticket = alert({
  fingerprint: "f-backup",
  name: "BackupTooOld",
  severity: "ticket",
  summary: "the last full backup is older than 8 days",
  node: null,
  service: null,
  starts_at: "2026-11-11T08:00:00Z",
  runbook: "docs/runbooks/alerts.md#backuptooold",
});

const alertsRoute = (child = <AlertsPage />) => [
  {
    path: "/operator",
    element: <OperatorLayout />,
    children: [
      { path: "alerts", element: child },
      { index: true, element: <p>queue</p> },
    ],
  },
];

afterEach(() => {
  vi.useRealTimers();
});

describe("alerts helpers", () => {
  it("groups by severity, page first, keeping the server's order", () => {
    const groups = groupBySeverity(
      [
        ticket,
        alert(),
        alert({ fingerprint: "x", name: "Other", severity: "info" }),
        alert({ fingerprint: "y", name: "Bare", severity: null }),
        alert({ fingerprint: "z", name: "NATSServerDown" }),
      ].map((a) => operatorAlerts.shape.alerts.element.parse(a)),
    );
    expect(groups.map((g) => [g.label, g.alerts.map((a) => a.name)])).toEqual([
      ["Page", ["ServiceDown", "NATSServerDown"]],
      ["Ticket", ["BackupTooOld"]],
      ["info", ["Other"]],
      ["No severity", ["Bare"]],
    ]);
    expect(criticalCount(groups.flatMap((g) => g.alerts))).toBe(2);
  });

  it("says how long an alert has fired", () => {
    const now = Date.parse("2026-11-13T12:00:00Z");
    expect(firingFor("2026-11-13T11:59:30Z", now)).toBe("less than a minute");
    expect(firingFor("2026-11-13T12:00:30Z", now)).toBe("less than a minute");
    expect(firingFor("2026-11-13T11:56:00Z", now)).toBe("4 min");
    expect(firingFor("2026-11-13T10:00:00Z", now)).toBe("2 h");
    expect(firingFor("2026-11-13T09:55:00Z", now)).toBe("2 h 5 min");
    expect(firingFor("2026-11-10T08:00:00Z", now)).toBe("3 d 4 h");
  });

  it("links only runbook paths of the repository", () => {
    expect(runbookUrl("docs/runbooks/alerts.md#servicedown")).toBe(
      "https://github.com/justmert/veristar/blob/main/docs/runbooks/alerts.md#servicedown",
    );
    expect(runbookUrl(null)).toBeNull();
    expect(runbookUrl("https://evil.example/x")).toBeNull();
    expect(runbookUrl("docs/runbooks/alerts.md")).toBeNull();
    expect(runbookUrl("docs/runbooks/../../x.md#a")).toBeNull();
  });

  it("polls every 30 seconds, also in a background tab", () => {
    expect(ALERTS_REFRESH_MS).toBe(30_000);
    expect(operatorAlertsQuery.refetchInterval).toBe(30_000);
    expect(operatorAlertsQuery.refetchIntervalInBackground).toBe(true);
  });

  it("refuses an answer without any source", () => {
    expect(operatorAlerts.safeParse(body({ sources: [] })).success).toBe(false);
    expect(operatorAlerts.safeParse(body({ alerts: [alert({ state: "firing" })] })).success).toBe(
      false,
    );
  });
});

describe("operator alerts page", () => {
  it("lists firing alerts grouped by severity with runbook links and how long they fire", async () => {
    const calls = stubFetch([
      { status: 200, body: f.operatorMeBody },
      { status: 200, body: body({ alerts: [alert(), ticket] }) },
    ]);
    const host = await renderRoutes(alertsRoute(), "/operator/alerts");
    await waitFor(() => expect(host.textContent).toContain("ServiceDown"));
    expect(calls.map((c) => c.url)).toEqual(["/api/me", "/api/operator/alerts"]);

    const headings = [...host.querySelectorAll("h3")].map((h) => h.textContent);
    expect(headings).toEqual(["Page, act now (1)", "Ticket, act within a working day (1)"]);
    const down = host.querySelector("[data-alert=ServiceDown]") as HTMLElement;
    expect(down.textContent).toContain("controlplane on core-1 does not answer scrapes");
    expect(down.textContent).toContain("Nodecore-1");
    expect(down.textContent).toContain("Servicecontrolplane");
    expect(down.textContent).toMatch(/Firing for \d+ (d|h|min)/);
    expect(down.querySelector("a")?.getAttribute("href")).toBe(
      "https://github.com/justmert/veristar/blob/main/docs/runbooks/alerts.md#servicedown",
    );
    expect(host.textContent).toContain("Last checked 12:00:05 UTC");
    expect(host.textContent).not.toContain("All clear");

    // the nav badge counts the alerts and the banner names the critical one
    const nav = host.querySelector('nav[aria-label="Operator sections"]') as HTMLElement;
    const link = [...nav.querySelectorAll("a")].find((a) => a.textContent?.startsWith("Alerts"));
    expect(link?.textContent).toBe("Alerts2 alerts firing");
    expect(host.textContent).toContain("A critical alert is firing.");
  });

  it("says all clear with the time of the check when nothing fires", async () => {
    stubFetch([
      { status: 200, body: f.operatorMeBody },
      { status: 200, body: body() },
    ]);
    const host = await renderRoutes(alertsRoute(), "/operator/alerts");
    await waitFor(() => expect(host.textContent).toContain("All clear"));
    expect(host.textContent).toContain("No alert is firing. Last checked 12:00:05 UTC.");
    expect(host.textContent).not.toContain("critical alert");
    expect(host.querySelector("[data-alert]")).toBeNull();
  });

  it("shows the alerting pipeline as down when the watchdog is missing, and never all clear", async () => {
    stubFetch([
      { status: 200, body: f.operatorMeBody },
      {
        status: 200,
        body: body({ watchdog_firing: false, sources: [{ ...source, watchdog_firing: false }] }),
      },
    ]);
    const host = await renderRoutes(alertsRoute(), "/operator/alerts");
    await waitFor(() => expect(host.textContent).toContain("Alerting pipeline down"));
    expect(host.textContent).not.toContain("All clear");
    expect(host.textContent).toContain("this is not all clear");
  });

  it("names a member that did not answer", async () => {
    stubFetch([
      { status: 200, body: f.operatorMeBody },
      {
        status: 200,
        body: body({
          sources: [
            source,
            {
              address: "10.0.1.5:9093",
              reachable: false,
              watchdog_firing: false,
              error: "dial tcp 10.0.1.5:9093: connect: connection refused",
            },
          ],
          alerts: [ticket],
        }),
      },
    ]);
    const host = await renderRoutes(alertsRoute(), "/operator/alerts");
    await waitFor(() => expect(host.textContent).toContain("1 of 2 Alertmanager members"));
    expect(host.textContent).toContain("10.0.1.5:9093: dial tcp");
    expect(host.textContent).toContain("BackupTooOld");
    expect(host.textContent).not.toContain("critical alert");
  });

  it("shows alert source unreachable and no list when no member answers", async () => {
    stubFetch([
      { status: 200, body: f.operatorMeBody },
      { status: 503, body: f.errorBody("alert_source_unreachable") },
    ]);
    const host = await renderRoutes(alertsRoute(), "/operator/alerts");
    await waitFor(() => expect(host.textContent).toContain("Alert source unreachable"));
    expect(host.textContent).not.toContain("All clear");
    expect(host.querySelector("[data-alert]")).toBeNull();
    const nav = host.querySelector('nav[aria-label="Operator sections"]') as HTMLElement;
    expect(nav.textContent).toContain("alert source unreachable");
  });

  it("says when no alert source is configured", async () => {
    stubFetch([
      { status: 200, body: f.operatorMeBody },
      { status: 503, body: f.errorBody("alert_source_not_configured") },
    ]);
    const host = await renderRoutes(alertsRoute(), "/operator/alerts");
    await waitFor(() => expect(host.textContent).toContain("No alert source configured"));
    expect(host.textContent).toContain("ALERTMANAGER_URLS");
  });

  it("refreshes every 30 seconds and on demand", async () => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
    const calls = stubFetch([
      { status: 200, body: f.operatorMeBody },
      { status: 200, body: body() },
      { status: 200, body: body({ alerts: [ticket], checked_at: "2026-11-13T12:00:35Z" }) },
      { status: 200, body: body({ checked_at: "2026-11-13T12:00:40Z" }) },
    ]);
    const host = await renderRoutes(alertsRoute(), "/operator/alerts");
    await waitFor(() => expect(host.textContent).toContain("All clear"));
    await act(async () => {
      vi.advanceTimersByTime(ALERTS_REFRESH_MS);
    });
    await waitFor(() => expect(host.textContent).toContain("BackupTooOld"));
    expect(host.textContent).toContain("Last checked 12:00:35 UTC");
    await click(byRole(host, "button", "Check now"));
    await waitFor(() => expect(host.textContent).toContain("Last checked 12:00:40 UTC"));
    expect(calls.filter((c) => c.url === "/api/operator/alerts")).toHaveLength(3);
  });
});

describe("operator banner and tenants", () => {
  it("shows the critical banner on the other operator pages", async () => {
    stubFetch([
      { status: 200, body: f.operatorMeBody },
      { status: 200, body: body({ alerts: [alert(), alert({ fingerprint: "g", name: "X" })] }) },
    ]);
    const host = await renderRoutes(alertsRoute(), "/operator");
    await waitFor(() => expect(host.textContent).toContain("2 critical alerts are firing."));
    expect(host.textContent).toContain("queue");
    const open = [...host.querySelectorAll("a")].find((a) => a.textContent === "Open alerts");
    expect(open?.getAttribute("href")).toBe("/operator/alerts");
  });

  it("shows no banner for ticket alerts", async () => {
    stubFetch([
      { status: 200, body: f.operatorMeBody },
      { status: 200, body: body({ alerts: [ticket] }) },
    ]);
    const host = await renderRoutes(alertsRoute(), "/operator");
    await waitFor(() => expect(host.textContent).toContain("1 alert firing"));
    expect(host.textContent).not.toContain("critical alert");
  });

  it("never asks for alerts in a tenant's browser", async () => {
    const calls = stubFetch([{ status: 200, body: f.meBody }]);
    const host = await renderRoutes(alertsRoute(), "/operator/alerts");
    await waitFor(() => expect(host.textContent).toContain("Operators only"));
    expect(host.textContent).not.toContain("Alerts");
    expect(calls.map((c) => c.url)).toEqual(["/api/me"]);
  });
});
