// the design verification, "end-to-end (playwright)" and "authorization":
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

// the action buttons of the detail page, then the dialog's reason and confirm
async function act(action: string, reason: string | null) {
  const page = op.page;
  await page.getByRole("region", { name: "Actions" }).getByRole("button", { name: action }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  if (reason !== null) await dialog.getByLabel("Reason").fill(reason);
  await dialog.getByRole("button", { name: action, exact: true }).click();
  await expect(dialog).toBeHidden();
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
  await page.goto(`${APP}/`);
  await page.getByRole("link", { name: "Operator", exact: true }).click();
  await page.getByRole("link", { name: "Settings" }).click();
  if (before.body.mode === "approval") {
    // a previous attempt switched already; go through the dialog both ways
    await page.getByRole("button", { name: "Switch to open" }).click();
    await page.getByRole("dialog").getByRole("button", { name: "Switch mode" }).click();
    await expect(page.getByText("Current mode: Open")).toBeVisible();
  }
  await page.getByRole("button", { name: "Switch to approval required" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toContainText("new enrollments only");
  // cancelling changes nothing
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(page.getByText("Current mode: Open")).toBeVisible();
  await page.getByRole("button", { name: "Switch to approval required" }).click();
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
  await page.getByRole("link").filter({ hasText: tenant.name }).click();
  await page.waitForURL(`${APP}/operator/tenants/${tenant.id}`);
  // enrollment answers and the site with its evidence
  await expect(page.getByText("Soroban contracts on testnet", { exact: true })).toBeVisible();
  await expect(page.getByRole("article", { name: site.name })).toContainText(site.domain);
  await expect(page.getByRole("article", { name: site.name })).toContainText("stellar.toml");
  const actions = page.getByRole("region", { name: "Actions" });
  await expect(actions.getByRole("button")).toHaveText(["Approve", "Reject", "Remove"]);

  await act("Approve", "Looks good");
  await expect(actions.getByRole("button")).toHaveText(["Suspend", "Remove"]);

  await waitProvisioned(owner.ctx, tenant.id);
  expect((await ownerStatus()).body.status).toBe("active");
  await owner.page.reload();
  await expect(owner.page.getByText("Waiting for review")).toHaveCount(0);
});

test("suspend needs a reason, which the operator then sees", async () => {
  const page = op.page;
  const actions = page.getByRole("region", { name: "Actions" });
  await actions.getByRole("button", { name: "Suspend" }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("button", { name: "Suspend", exact: true }).click();
  await expect(dialog).toContainText("Required.");
  await dialog.getByLabel("Reason").fill("Unusual traffic, contact us");
  await dialog.getByRole("button", { name: "Suspend", exact: true }).click();
  await expect(dialog).toBeHidden();
  await expect(actions.getByRole("button")).toHaveText(["Resume", "Remove"]);

  const t = await ownerStatus();
  expect(t.body.status).toBe("suspended");
  expect(t.body.status_reason).toBe("Unusual traffic, contact us");
  await owner.page.goto(`${APP}/projects/${tenant.id}`);
  await expect(owner.page.getByText("This project is suspended")).toBeVisible();
  await expect(owner.page.getByText("Reason given: Unusual traffic, contact us")).toBeVisible();
});

test("resume returns the project to active", async () => {
  await act("Resume", null);
  await expect(op.page.getByRole("region", { name: "Actions" }).getByRole("button")).toHaveText([
    "Suspend",
    "Remove",
  ]);
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
  await expect(op.page.getByText("No action is possible from this status.")).toBeVisible();
  await expect(op.page.getByRole("region", { name: "Removal" })).toBeVisible();

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
  await expect(owner.page.getByText("Reason given: Not a Stellar project")).toBeVisible();
  await owner.page.goto(`${APP}/`);
  await expect(owner.page.getByRole("list", { name: "Removed projects" })).toContainText(
    tenant.name,
  );

  // the audit log, filtered to this project in the console
  const page = op.page;
  await page.getByRole("link", { name: "Full audit log of this project" }).click();
  const entries = page.getByRole("list", { name: "Audit entries" });
  for (const action of ["tenant.approve", "tenant.suspend", "tenant.resume", "tenant.remove"])
    await expect(entries).toContainText(action);
  await expect(entries).toContainText("Reason: Unusual traffic, contact us");
  await expect(entries).toContainText("Reason: Not a Stellar project");
  await page.getByLabel("Action").fill("settings.");
  await page.getByLabel("Project id").fill("");
  await page.getByRole("button", { name: "Apply filters" }).click();
  await expect(entries).toContainText("settings.enrollment_mode");
});
