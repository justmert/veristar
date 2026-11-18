// the operator alerts page (contract 11), end to end: alerts reach a real
// alertmanager through its v2 api, as prometheus delivers them, and the
// control plane reads them over mutual tls. an operator sees them grouped by
// severity with runbook links, a count badge in the operator nav and the
// critical banner on the other operator pages; all clear once they resolve;
// the alerting pipeline as down once the watchdog stops; and "alert source
// unreachable" while alertmanager is stopped. a tenant never sees the page
// and gets 403 from the route.
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import type { BrowserContext, Page } from "@playwright/test";
import { APP, APPS_CTL, OUT } from "../fixture/env.ts";
import { api, type GitHubUser, newUser, operatorStepUp, signIn } from "./helpers.ts";
import { expect, test } from "./test.ts";

test.describe.configure({ mode: "serial" });

const OPERATOR: GitHubUser = { id: 1, login: "sa-operator" };
const shots = join(OUT, "operator-alerts");

let op: { ctx: BrowserContext; page: Page };
let tenant: { ctx: BrowserContext; page: Page };
const problems: string[] = [];

async function ctl(path: string, body?: unknown): Promise<void> {
  const r = await fetch(`${APPS_CTL}${path}`, {
    method: "POST",
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  expect(r.status, await r.text()).toBe(204);
}

interface Rule {
  name: string;
  severity: string;
  summary: string;
  labels?: Record<string, string>;
  startedMinutesAgo: number;
}

// a postableAlert as prometheus sends it. a firing alert ends in the
// future (prometheus resends it every evaluation); a resolved one ends now.
function postable(r: Rule, firing: boolean) {
  const now = Date.now();
  return {
    labels: { alertname: r.name, severity: r.severity, ...r.labels },
    annotations: {
      summary: r.summary,
      runbook: `docs/runbooks/alerts.md#${r.name.toLowerCase()}`,
    },
    startsAt: new Date(now - r.startedMinutesAgo * 60_000).toISOString(),
    endsAt: new Date(firing ? now + 3_600_000 : now - 1_000).toISOString(),
    generatorURL: "http://prometheus:9090/graph",
  };
}

const WATCHDOG: Rule = {
  name: "Watchdog",
  severity: "heartbeat",
  summary: "the alerting pipeline works",
  startedMinutesAgo: 600,
};
// alertmanager keeps the earliest start of an alert it already holds, so a
// retried test sees the start of the first attempt: durations are matched
// loosely
const SERVICE_DOWN: Rule = {
  name: "ServiceDown",
  severity: "page",
  summary: "controlplane on core-2 does not answer scrapes",
  labels: { node: "core-2", job: "controlplane", instance: "10.0.1.5:9303" },
  startedMinutesAgo: 125,
};
const BACKUP_TOO_OLD: Rule = {
  name: "BackupTooOld",
  severity: "ticket",
  summary: "the last full backup is older than 8 days",
  startedMinutesAgo: 30,
};

async function send(firing: Rule[], resolved: Rule[] = []) {
  await ctl("/alertmanager/alerts", [
    ...firing.map((r) => postable(r, true)),
    ...resolved.map((r) => postable(r, false)),
  ]);
}

const alertsUrl = `${APP}/operator/alerts`;

test.beforeAll(async ({ browser }) => {
  test.setTimeout(300_000);
  mkdirSync(shots, { recursive: true });
  const octx = await browser.newContext();
  op = { ctx: octx, page: await octx.newPage() };
  op.page.on("pageerror", (e) => problems.push(`pageerror: ${e.message}`));
  await signIn(op.page, OPERATOR);
  await operatorStepUp(op.page, OPERATOR);
  const me = await api<{ user: { is_operator: boolean } }>(op.ctx, "GET", "/api/me");
  expect(me.body.user.is_operator, "github id 1 must be an operator").toBe(true);

  const tctx = await browser.newContext();
  tenant = { ctx: tctx, page: await tctx.newPage() };
  await signIn(tenant.page, newUser("alertten"));
});

test.afterAll(async () => {
  await op?.ctx.close();
  await tenant?.ctx.close();
});

test("an operator sees firing alerts by severity, the badge and the critical banner", async () => {
  await send([WATCHDOG, SERVICE_DOWN, BACKUP_TOO_OLD]);
  const page = op.page;
  await page.goto(alertsUrl);

  await expect(page.getByRole("heading", { level: 1, name: /^Alerts/ })).toBeVisible();
  const pageGroup = page.getByRole("region", { name: /^Act now 1$/ });
  const ticketGroup = page.getByRole("region", { name: /^Act within a working day 1$/ });
  await expect(pageGroup).toBeVisible();
  await expect(ticketGroup).toBeVisible();

  const down = pageGroup.locator('[data-alert="ServiceDown"]');
  // the title in sentence case; the labels follow as a secondary line
  await expect(down).toContainText("Controlplane on core-2 does not answer scrapes");
  await expect(down).toContainText(/Fires for 2 h \d+ min/);
  await expect(down).toContainText("core-2");
  await expect(down).toContainText("10.0.1.5:9303");
  await expect(down.getByRole("link", { name: /ServiceDown/ })).toHaveAttribute(
    "href",
    "https://github.com/justmert/veristar/blob/main/docs/runbooks/alerts.md#servicedown",
  );
  await expect(ticketGroup.locator('[data-alert="BackupTooOld"]')).toContainText(
    /Fires for 3\d min/,
  );
  // the watchdog is the pipeline's heartbeat, never a listed alert
  await expect(page.locator('[data-alert="Watchdog"]')).toHaveCount(0);
  await expect(page.getByText("The alert checks do not run", { exact: true })).toHaveCount(0);
  await expect(page.getByText(/^Last checked \d\d:\d\d:\d\d UTC$/)).toBeVisible();

  const nav = page.getByRole("navigation", { name: "Operator" });
  await expect(nav.getByRole("link", { name: /^Alerts\s*1 critical alert firing$/ })).toBeVisible();
  await page.screenshot({ path: join(shots, "firing.png"), fullPage: true });

  // the banner follows the operator to the other pages of the area
  await page.goto(`${APP}/operator/projects`);
  await expect(
    page.getByRole("alert").filter({ hasText: "1 critical alert is firing." }),
  ).toBeVisible();
  await page.getByRole("link", { name: "Open alerts" }).click();
  await expect(page).toHaveURL(alertsUrl);
});

test("all clear once the alerts resolve, with the time of the check", async () => {
  await send([WATCHDOG], [SERVICE_DOWN, BACKUP_TOO_OLD]);
  const page = op.page;
  await page.goto(alertsUrl);
  await expect(page.getByText("All clear", { exact: true })).toBeVisible();
  await expect(page.getByText(/^No alert fires\./)).toBeVisible();
  await expect(page.getByText(/^Last checked \d\d:\d\d:\d\d UTC$/)).toBeVisible();
  await expect(page.locator("[data-alert]")).toHaveCount(0);
  await expect(page.getByText("critical alert")).toHaveCount(0);
  await page.screenshot({ path: join(shots, "all-clear.png"), fullPage: true });
});

test("the page refreshes by itself and shows a new alert within 30 seconds", async () => {
  const page = op.page;
  await page.goto(alertsUrl);
  await expect(page.getByText("All clear", { exact: true })).toBeVisible();
  await send([WATCHDOG, BACKUP_TOO_OLD]);
  // no reload and no click: the 30 second poll brings it
  await expect(page.locator('[data-alert="BackupTooOld"]')).toBeVisible({ timeout: 40_000 });
  await send([WATCHDOG], [BACKUP_TOO_OLD]);
});

test("a missing watchdog shows the alerting pipeline as down, never all clear", async () => {
  await send([], [WATCHDOG]);
  const page = op.page;
  await page.goto(alertsUrl);
  await expect(page.getByText("The alert checks do not run", { exact: true })).toBeVisible();
  await expect(page.getByText("All clear", { exact: true })).toHaveCount(0);
  await expect(page.getByText(/An alert can fire without showing here/).first()).toBeVisible();
  await page.screenshot({ path: join(shots, "pipeline-down.png"), fullPage: true });
});

test("a tenant never sees the page and gets 403 from the route", async () => {
  await send([WATCHDOG, SERVICE_DOWN]);
  const r = await api<{ error?: { code?: string } }>(tenant.ctx, "GET", "/api/operator/alerts");
  expect(r.status).toBe(403);
  expect(r.body?.error?.code).toBe("operator_required");
  await tenant.page.goto(alertsUrl);
  await expect(tenant.page.getByRole("heading", { name: "Operators only" })).toBeVisible();
  await expect(tenant.page.getByText("ServiceDown")).toHaveCount(0);
  await expect(tenant.page.getByText("critical alert")).toHaveCount(0);
  await send([WATCHDOG], [SERVICE_DOWN]);
});

test("alert source unreachable while alertmanager is down, and no list", async () => {
  await ctl("/alertmanager/stop");
  try {
    const page = op.page;
    await page.goto(alertsUrl);
    await expect(
      page.getByText("The alert service does not answer", { exact: true }),
    ).toBeVisible();
    await expect(page.getByText("All clear", { exact: true })).toHaveCount(0);
    await expect(page.locator("[data-alert]")).toHaveCount(0);
    const nav = page.getByRole("navigation", { name: "Operator" });
    await expect(nav.getByRole("link", { name: /alert source did not answer/ })).toBeVisible();
    const r = await api<{ error?: { code?: string } }>(op.ctx, "GET", "/api/operator/alerts");
    expect(r.status).toBe(503);
    expect(r.body?.error?.code).toBe("alert_source_unreachable");
    await page.screenshot({ path: join(shots, "unreachable.png"), fullPage: true });
  } finally {
    await ctl("/alertmanager/start");
  }
  // the restarted member holds nothing until prometheus sends again
  await send([WATCHDOG]);
  await op.page.goto(alertsUrl);
  await expect(op.page.getByText("All clear", { exact: true })).toBeVisible();
  expect(problems, "page errors").toEqual([]);
});
