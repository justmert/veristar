// operator governance end to end, with its authorization:
// an operator switches enrollment to approval, reviews a pending project in
// the console, approves, suspends, resumes and removes it, each with a reason
// and a confirmation, and the project's owner sees every status with the
// operator's reason. a non-operator gets 403 operator_required on every
// operator route and the console shows the area as operators only. the
// operator is github id 1, the only id in OPERATOR_GITHUB_IDS (stack_test.go).
import { randomUUID } from "node:crypto";
import type { BrowserContext, Page } from "@playwright/test";
import { APP } from "../fixture/env.ts";
import {
  addSiteApi,
  api,
  enrollApi,
  type GitHubUser,
  newUser,
  operatorStepUp,
  type Site,
  signIn,
  type Tenant,
  uniqueHost,
  waitProvisioned,
} from "./helpers.ts";
import { expect, test } from "./test.ts";

test.describe.configure({ mode: "serial" });

const OPERATOR: GitHubUser = { id: 1, login: "sa-operator" };

let op: { ctx: BrowserContext; page: Page };
let owner: { ctx: BrowserContext; page: Page; user: GitHubUser };
let tenant: Tenant;
let site: Site;

interface Envelope {
  error?: { code?: string };
  status_reason?: string | null;
}

test.beforeAll(async ({ browser }) => {
  const octx = await browser.newContext();
  op = { ctx: octx, page: await octx.newPage() };
  await signIn(op.page, OPERATOR);
  await operatorStepUp(op.page, OPERATOR);
  const me = await api<{ user: { is_operator: boolean } }>(op.ctx, "GET", "/api/me");
  expect(me.body.user.is_operator, "github id 1 must be an operator").toBe(true);

  const tctx = await browser.newContext();
  owner = { ctx: tctx, page: await tctx.newPage(), user: newUser("opown") };
  await signIn(owner.page, owner.user);
});

test.afterAll(async () => {
  // the stack starts in open mode (00004_seed.sql) and the other specs enroll
  // in it. a retry runs in a fresh worker that cannot know what an earlier
  // attempt found, so the seeded value is restored, not a remembered one.
  if (op) {
    const r = await api(op.ctx, "PUT", "/api/operator/settings/enrollment-mode", {
      mode: "open",
    });
    expect(r.status).toBe(200);
  }
  await op?.ctx.close();
  await owner?.ctx.close();
});

// suspend, resume and remove sit in the actions menu of the project page;
// each opens the confirmation with its reason (and for remove the slug)
async function act(action: string, reason: string | null) {
  const page = op.page;
  await page.getByRole("button", { name: "Actions", exact: true }).click();
  await page.getByRole("menuitem", { name: `${action} project` }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  if (reason !== null) await dialog.getByLabel("Reason").fill(reason);
  if (action === "Remove") await dialog.getByLabel(/^Type .* to confirm$/).fill(tenant.slug);
  await dialog.getByRole("button", { name: `${action} project`, exact: true }).click();
  await expect(dialog).toBeHidden();
}

// the actions the menu of the project page offers
async function menuActions(): Promise<string[]> {
  const page = op.page;
  const trigger = page.getByRole("button", { name: "Actions", exact: true });
  if ((await trigger.count()) === 0) return [];
  await trigger.click();
  const items = await page.getByRole("menuitem").allTextContents();
  await page.keyboard.press("Escape");
  return items;
}

async function ownerStatus(): Promise<{ status: number; body: Tenant & Envelope }> {
  return api<Tenant & Envelope>(owner.ctx, "GET", `/api/tenants/${tenant.id}`);
}

test("a non-operator gets 403 operator_required on every operator route", async () => {
  const id = randomUUID();
  const routes: [string, string, unknown][] = [
    ["GET", "/api/operator/tenants", undefined],
    ["GET", "/api/operator/tenants?review=true", undefined],
    ["GET", `/api/operator/tenants/${id}`, undefined],
    ["POST", `/api/operator/tenants/${id}/approve`, {}],
    ["POST", `/api/operator/tenants/${id}/reject`, { reason: "x" }],
    ["POST", `/api/operator/tenants/${id}/suspend`, { reason: "x" }],
    ["POST", `/api/operator/tenants/${id}/resume`, {}],
    ["POST", `/api/operator/tenants/${id}/remove`, { reason: "x" }],
    ["POST", `/api/operator/sites/${id}/approve`, {}],
    ["POST", `/api/operator/sites/${id}/reject`, { reason: "x" }],
    ["GET", "/api/operator/settings/enrollment-mode", undefined],
    ["PUT", "/api/operator/settings/enrollment-mode", { mode: "open" }],
    ["GET", "/api/operator/settings/removal-keep-aggregates", undefined],
    ["PUT", "/api/operator/settings/removal-keep-aggregates", { keep: false }],
    ["GET", "/api/operator/audit", undefined],
  ];
  const failures: string[] = [];
  for (const [method, path, body] of routes) {
    const r = await api<Envelope>(owner.ctx, method, path, body);
    if (r.status !== 403 || r.body?.error?.code !== "operator_required")
      failures.push(`${method} ${path}: ${r.status} ${r.body?.error?.code}`);
  }
  expect(failures, "operator routes that did not answer 403 operator_required").toEqual([]);

  // the console keeps the area closed and shows no operator link
  await owner.page.goto(`${APP}/operator`);
  await expect(owner.page.getByRole("heading", { name: "Operators only" })).toBeVisible();
  await expect(owner.page.getByRole("link", { name: "Operator", exact: true })).toHaveCount(0);
});

test("the operator switches enrollment to approval after a confirmation", async () => {
  const before = await api<{ mode: string }>(
    op.ctx,
    "GET",
    "/api/operator/settings/enrollment-mode",
  );
  expect(before.status).toBe(200);
  const page = op.page;
  await page.goto(`${APP}/operator`);
  await page
    .getByRole("navigation", { name: "Operator" })
    .getByRole("link", { name: "Settings" })
    .click();
  // the enrollment mode is a radio group; change setting asks for confirmation
  const mode = page.getByRole("region", { name: "Enrollment mode" });
  if (before.body.mode === "approval") {
    // a previous attempt switched already; go through the dialog both ways
    await mode.getByRole("radio", { name: "Open" }).check();
    await mode.getByRole("button", { name: "Change setting" }).click();
    await page.getByRole("dialog").getByRole("button", { name: "Switch mode" }).click();
    await expect(page.getByText("Current mode: Open")).toBeVisible();
  }
  await mode.getByRole("radio", { name: "Approval required" }).check();
  await mode.getByRole("button", { name: "Change setting" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toContainText("new enrollments only");
  // cancelling changes nothing
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(page.getByText("Current mode: Open")).toBeVisible();
  await mode.getByRole("radio", { name: "Approval required" }).check();
  await mode.getByRole("button", { name: "Change setting" }).click();
  await page.getByRole("dialog").getByRole("button", { name: "Switch mode" }).click();
  await expect(page.getByText("Current mode: Approval required")).toBeVisible();
  const after = await api<{ mode: string }>(
    op.ctx,
    "GET",
    "/api/operator/settings/enrollment-mode",
  );
  expect(after.body.mode).toBe("approval");
});

test("a new project waits in the review queue and the operator approves it", async () => {
  tenant = await enrollApi(owner.ctx, `review ${owner.user.login}`);
  expect(tenant.status).toBe("pending");
  site = await addSiteApi(owner.ctx, tenant.id, uniqueHost("opreview"));

  await owner.page.goto(`${APP}/projects/${tenant.id}`);
  await expect(owner.page.getByText("Waiting for review")).toBeVisible();

  const page = op.page;
  await page.goto(`${APP}/operator`);
  // the review workspace: pick the project in the queue, its evidence and
  // the decision show beside it, the project page holds every action
  await expect(page.getByRole("heading", { level: 1, name: "Review" })).toBeVisible();
  const queue = page.getByRole("navigation", { name: "Review queue" });
  await queue.getByRole("button").filter({ hasText: tenant.name }).click();
  const review = page.getByRole("region", { name: `Review ${tenant.name}` });
  await expect(review.getByRole("list", { name: "Evidence" })).toBeVisible();
  await expect(review.getByRole("radiogroup", { name: "Decision" })).toBeVisible();
  // a new project has no figures yet: the evidence carries the decision
  await expect(review.locator("[data-numbers]")).toHaveCount(0);
  await review.getByRole("link", { name: "Open the project page" }).click();
  await page.waitForURL(`${APP}/operator/projects/${tenant.id}`);
  // enrollment answers and the site with its evidence
  await expect(page.getByText("Soroban contracts on testnet", { exact: true })).toBeVisible();
  const siteRow = page
    .getByRole("table", { name: "Sites" })
    .getByRole("row")
    .filter({ hasText: site.name });
  await expect(siteRow).toContainText(site.domain);
  await expect(siteRow).toContainText("stellar.toml");
  // one decision panel for approve and reject; remove waits in the menu
  const decision = page.getByRole("form", { name: `Decision on ${tenant.name}` });
  await expect(decision.getByRole("radio")).toHaveText([/Approve/, /Reject/]);
  expect(await menuActions()).toEqual(["Remove project"]);

  // nothing is chosen on open, so the send waits for a choice
  await expect(decision.getByRole("button", { name: "Choose Approve or Reject" })).toBeDisabled();
  await decision.getByRole("radio", { name: /Approve/ }).click();
  await decision.getByLabel(/^Reason/).fill("Looks good");
  await decision.getByRole("button", { name: /^Approve project/ }).click();
  await expect(page.getByText(`${tenant.name} approved.`)).toBeVisible();
  await expect(decision).toHaveCount(0);
  expect(await menuActions()).toEqual(["Suspend project", "Remove project"]);

  await waitProvisioned(owner.ctx, tenant.id);
  expect((await ownerStatus()).body.status).toBe("active");
  await owner.page.reload();
  await expect(owner.page.getByText("Waiting for review")).toHaveCount(0);
});

test("suspend needs a reason, which the tenant owner then sees", async () => {
  const page = op.page;
  await page.getByRole("button", { name: "Actions", exact: true }).click();
  await page.getByRole("menuitem", { name: "Suspend project" }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("button", { name: "Suspend project", exact: true }).click();
  await expect(dialog).toContainText("Required.");
  await dialog.getByLabel("Reason").fill("Unusual traffic, contact us");
  await dialog.getByRole("button", { name: "Suspend project", exact: true }).click();
  await expect(dialog).toBeHidden();
  await expect(page.getByText(`${tenant.name} is suspended.`)).toBeVisible();
  expect(await menuActions()).toEqual(["Resume project", "Remove project"]);

  const t = await ownerStatus();
  expect(t.body.status).toBe("suspended");
  expect(t.body.status_reason).toBe("Unusual traffic, contact us");
  await owner.page.goto(`${APP}/projects/${tenant.id}`);
  await expect(owner.page.getByText("This project is suspended")).toBeVisible();
  await expect(owner.page.getByText("Reason given: “Unusual traffic, contact us”")).toBeVisible();
});

test("resume returns the project to active", async () => {
  await act("Resume", null);
  expect(await menuActions()).toEqual(["Suspend project", "Remove project"]);
  const t = await ownerStatus();
  expect(t.body.status).toBe("active");
  expect(t.body.status_reason).toBeNull();
  await owner.page.goto(`${APP}/projects/${tenant.id}`);
  // the project page rendered (the sidebar names the project; the page has its own h1)
  await expect(owner.page.getByRole("heading", { level: 1 })).toBeVisible();
  await expect(owner.page.getByText("This project is suspended")).toHaveCount(0);
});

test("remove takes the project away from its members and keeps the audit trail", async () => {
  await act("Remove", "Not a Stellar project");
  await expect(op.page.getByRole("region", { name: "Removal" })).toBeVisible();
  expect(await menuActions()).toEqual([]);

  const t = await ownerStatus();
  expect(t.status).toBe(404);
  expect(t.body.error?.code).toBe("not_found");
  const me = await api<{ removed_tenants: { id: string; status_reason: string | null }[] }>(
    owner.ctx,
    "GET",
    "/api/me",
  );
  expect(me.body.removed_tenants).toContainEqual(
    expect.objectContaining({ id: tenant.id, status_reason: "Not a Stellar project" }),
  );
  await owner.page.goto(`${APP}/projects/${tenant.id}`);
  await expect(owner.page.getByText("This project was removed by an operator")).toBeVisible();
  await expect(owner.page.getByText("Reason given: “Not a Stellar project”")).toBeVisible();
  await owner.page.goto(`${APP}/`);
  await expect(owner.page.getByRole("list", { name: "Removed projects" })).toContainText(
    tenant.name,
  );

  // the audit log, filtered to this project in the console
  const page = op.page;
  await page.getByRole("link", { name: "Open the full audit log" }).click();
  const entries = page.getByRole("list", { name: "Audit entries" });
  // each entry reads as a sentence; the action code stays in its details
  for (const verb of ["approved", "suspended", "resumed", "removed"])
    await expect(entries).toContainText(`${OPERATOR.login} ${verb}`);
  for (const action of ["tenant.approve", "tenant.suspend", "tenant.resume", "tenant.remove"])
    await expect(entries).toContainText(action);
  await expect(entries).toContainText("Reason: Unusual traffic, contact us");
  await expect(entries).toContainText("Reason: Not a Stellar project");
  // the filters apply at once, with no apply button
  await page.getByRole("button", { name: /^Filters/ }).click();
  const sheet = page.getByRole("dialog", { name: "Filters" });
  await sheet.getByLabel("Action").selectOption("settings.");
  await sheet.getByLabel("Project id").fill("");
  await sheet.getByRole("button", { name: "Show results" }).click();
  await expect(entries).toContainText("changed the enrollment mode");
});
