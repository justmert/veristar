// the on-chain witness, the scf review and the csv exports, end to end. an
// owner declares an account in the console and sees it unverified with a
// challenge to prove it; the scf review and the overview export answer for
// the project; another project's member gets 404 on every route of it; an
// operator sees the on-chain column, exports exactly the rows on screen
// and reads the on-chain section of the project's detail.
//
// the offline stack reaches no ledger api, so no proof can succeed and no
// day is read here: what this spec checks is that the missing data shows
// as missing, never as zero, and that every route keeps to its tenant. the
// proofs and the daily counts are covered against recorded ledger answers
// in the go tests of internal/chain and internal/controlplane/onchain.
import type { BrowserContext, Page } from "@playwright/test";
import { APP } from "../fixture/env.ts";
import {
  api,
  enrollApi,
  type GitHubUser,
  newUser,
  operatorStepUp,
  signIn,
  type Tenant,
} from "./helpers.ts";
import { expect, test } from "./test.ts";

test.describe.configure({ mode: "serial" });

const OPERATOR: GitHubUser = { id: 1, login: "sa-operator" };
// a valid mainnet account id no other spec declares
const ACCOUNT = "GDWQ7YV27AVTL25BDOLZD6E44VCQLCEBJXBOOWA2R3VLKCUIZHSXZLDE";
const run = `oc${Math.random().toString(36).slice(2, 7)}`;

let owner: { ctx: BrowserContext; page: Page };
let tenant: Tenant;

test.beforeAll(async ({ browser }) => {
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  await signIn(page, newUser("onchain-owner"));
  owner = { ctx, page };
  tenant = await enrollApi(ctx, `On-chain ${run}`);
});

test.afterAll(async () => {
  await owner?.ctx.close();
});

test("an owner declares an account and gets a challenge to prove it", async () => {
  const { page } = owner;
  await page.goto(`${APP}/projects/${tenant.id}/onchain`);
  await expect(page.getByRole("heading", { name: "On-chain", level: 1 })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Accounts and contracts" })).toBeVisible();
  await page.getByRole("textbox", { name: "Account or contract" }).fill("not-a-key");
  await page.getByRole("button", { name: "Add", exact: true }).click();
  await expect(
    page.getByText("Enter a Stellar account (G…) or a Soroban contract (C…)"),
  ).toBeVisible();

  await page.getByRole("textbox", { name: "Account or contract" }).fill(ACCOUNT);
  await page.getByRole("button", { name: "Add", exact: true }).click();
  // the id shows in its short form, the whole id in its tooltip
  await expect(page.locator(`[data-strkey="${ACCOUNT}"]`)).toBeVisible();
  await expect(page.getByText("Unverified").first()).toBeVisible();

  await page.getByRole("button", { name: "Prove control" }).click();
  const message = page.locator("pre", { hasText: "Veristar ownership proof" });
  await expect(message).toContainText(`(${tenant.id})`);
  await expect(message).toContainText(`Account: ${ACCOUNT}`);
  await expect(page.getByText("Publish a data entry")).toBeVisible();

  // the view keeps a day without a stored row as missing with its reason
  const view = await api<{
    ids: {
      strkey: string;
      verification: { status: string };
      series: { operations: number | null; missing: string | null }[];
    }[];
  }>(owner.ctx, "GET", `/api/tenants/${tenant.id}/onchain`);
  expect(view.status).toBe(200);
  const id = view.body.ids.find((x) => x.strkey === ACCOUNT);
  expect(id?.verification.status).toBe("unverified");
  for (const p of id?.series ?? []) {
    expect(p.operations).toBeNull();
    expect(p.missing).not.toBeNull();
  }

  // the overview sums the on-chain activity up in one card, next to web
  // and api, and links to the on-chain page
  await page.goto(`${APP}/projects/${tenant.id}`);
  const card = page.getByRole("region", { name: "On-chain" });
  await expect(card).toContainText("0 of 1 verified");
  await expect(card.getByRole("link", { name: "On-chain" })).toHaveAttribute(
    "href",
    `/projects/${tenant.id}/onchain`,
  );

  // weekly active accounts: the project has no verified id and the task
  // has not counted a week of it, so every iso week of the range shows its
  // reason and none is a zero
  const weekly = await api<{
    totals: { weekly_active_accounts: { average: number | null; weeks_missing: number } };
    weeks: { week: string; active_accounts: number | null; missing: string | null }[];
  }>(owner.ctx, "GET", `/api/tenants/${tenant.id}/onchain`);
  expect(weekly.body.weeks.length).toBeGreaterThanOrEqual(3);
  for (const w of weekly.body.weeks) {
    expect(w.week).toMatch(/^\d{4}-W\d{2}$/);
    expect(w.active_accounts).toBeNull();
    expect(w.missing).not.toBeNull();
  }
  expect(weekly.body.totals.weekly_active_accounts.average).toBeNull();
  expect(weekly.body.totals.weekly_active_accounts.weeks_missing).toBe(weekly.body.weeks.length);
  // the on-chain page shows every week of its default range and why it
  // has no count, and the summary says that none was counted
  await page.goto(`${APP}/projects/${tenant.id}/onchain`);
  await expect(page.getByRole("heading", { name: "Weekly active accounts" })).toBeVisible();
  const weeks = page.getByRole("list", { name: "Weekly active accounts per ISO week" });
  await expect(weeks.locator("[data-week]")).toHaveCount(weekly.body.weeks.length);
  // every week misses for one reason, so the reason shows once under them
  await expect(weeks.locator("xpath=..").locator("p[data-missing]")).toBeVisible();
  await expect(weeks.locator("[data-week]").first()).not.toContainText(/\b0\b/);
  await expect(page.getByTestId("waa-summary")).toContainText("No complete week counted");
  // the project has no verified id, so its activity counts nothing yet
  await expect(page.getByTestId("activity-scope")).toContainText(
    "0 of 1 account or contract verified",
  );
});

test("scf review and the overview export answer for the project", async () => {
  const { page, ctx } = owner;
  await page.goto(`${APP}/projects/${tenant.id}/scf`);
  await expect(page.getByRole("heading", { name: "SCF review", level: 1 })).toBeVisible();
  const chain = page.getByRole("region", { name: "On-chain" });
  await expect(chain).toContainText("0 of 1");
  await expect(chain.getByText("Weekly active accounts", { exact: true })).toBeVisible();
  const href = await page.getByRole("link", { name: "Export CSV" }).getAttribute("href");
  expect(href).toMatch(new RegExp(`^/api/tenants/${tenant.id}/overview\\.csv\\?from=`));

  const r = await ctx.request.get(`${APP}${href}`);
  expect(r.status()).toBe(200);
  expect(r.headers()["content-type"]).toBe("text/csv; charset=utf-8");
  expect(r.headers()["content-disposition"]).toContain("attachment");
  const lines = (await r.text()).trim().split("\n");
  const header = lines[0]?.split(",") ?? [];
  expect(header.slice(0, 14)).toEqual([
    "day",
    "web_pageviews",
    "web_visitors",
    "web_visits",
    "web_bounces",
    "web_totaltime_seconds",
    "web_quarantine_pageviews",
    "api_requests",
    "api_errors_4xx",
    "api_errors_5xx",
    "api_infrastructure_requests",
    "onchain_ids_read",
    "onchain_operations",
    "onchain_transactions",
  ]);
  // one row per utc day of the range; an unread signal is empty, not zero
  expect(lines.length).toBe(31);
  const first = lines[1]?.split(",") ?? [];
  const col = (name: string) => first[header.indexOf(name)];
  expect([col("onchain_ids_read"), col("onchain_operations"), col("onchain_transactions")]).toEqual(
    ["0", "", ""],
  );
});

test("another project's member sees none of it", async ({ browser }) => {
  const ctx = await browser.newContext();
  try {
    const page = await ctx.newPage();
    await signIn(page, newUser("onchain-intruder"));
    await enrollApi(ctx, `Intruder ${run}`);
    const view = await api<{ ids: { id: string }[] }>(
      owner.ctx,
      "GET",
      `/api/tenants/${tenant.id}/onchain`,
    );
    const chainId = view.body.ids[0]?.id ?? "";
    for (const [method, path, body] of [
      ["GET", `/api/tenants/${tenant.id}/onchain`],
      ["GET", `/api/tenants/${tenant.id}/scf-view`],
      ["GET", `/api/tenants/${tenant.id}/overview.csv`],
      ["POST", `/api/tenants/${tenant.id}/onchain/ids`, { strkey: ACCOUNT }],
      ["POST", `/api/tenants/${tenant.id}/onchain/ids/${chainId}/challenge`, {}],
      ["DELETE", `/api/tenants/${tenant.id}/onchain/ids/${chainId}`],
    ] as [string, string, unknown?][]) {
      const r = await api(ctx, method, path, body);
      expect(r.status, `${method} ${path}`).toBe(404);
      expect(JSON.stringify(r.body)).not.toContain(ACCOUNT);
    }
    // nor do operator routes open for a non-operator
    const r = await api(ctx, "GET", `/api/operator/web/projects.csv`);
    expect(r.status).toBe(403);
  } finally {
    await ctx.close();
  }
});

test("an operator sees the column, exports the rows on screen and reads the detail", async ({
  browser,
}) => {
  const ctx = await browser.newContext();
  try {
    const page = await ctx.newPage();
    await signIn(page, OPERATOR);
    await operatorStepUp(page, OPERATOR);
    await page.goto(`${APP}/operator/projects?view=web&q=${encodeURIComponent(`On-chain ${run}`)}`);
    // the header names the column, then reads its definition
    await expect(page.getByRole("columnheader", { name: /^On-chain\W+Operations/ })).toBeVisible();
    const row = page.locator(`tr[data-tenant="${tenant.id}"]`);
    // the summary is read with the number: a tooltip on hover and focus
    await expect(row.locator("[data-col=onchain]")).toContainText("0 of 1 ID verified");

    const href = await page.getByRole("link", { name: "Export CSV" }).getAttribute("href");
    expect(href).toContain("/api/operator/web/projects.csv?");
    expect(href).toContain("rows=1");
    const r = await ctx.request.get(`${APP}${href}`);
    expect(r.status()).toBe(200);
    const lines = (await r.text()).trim().split("\n");
    expect(lines).toHaveLength(2);
    expect(lines[0]).toContain("onchain_operations");
    expect(lines[0]).toContain("onchain_weekly_active_accounts_average");
    expect(lines[0]).toContain("onchain_weeks_missing");
    expect(lines[1]?.startsWith(tenant.id)).toBe(true);

    // the export wrote an audit row with the query it answered
    const audit = await api<{ items: { action: string; detail: { view?: string } }[] }>(
      ctx,
      "GET",
      "/api/operator/audit?action=export.csv",
    );
    expect(audit.status).toBe(200);
    expect(audit.body.items.some((e) => e.detail.view === "web_projects")).toBe(true);

    await page.goto(`${APP}/operator/projects/${tenant.id}`);
    await page.getByText("On-chain IDs", { exact: true }).click();
    const section = page.locator("details#onchain");
    await expect(section).toContainText(ACCOUNT);
    await expect(section).toContainText("Unverified");
  } finally {
    await ctx.close();
  }
});

// calls per contract function (contract 15.9): a declared contract that is
// not verified counts nowhere, so it shows no calls and every day says why;
// an account has no functions. the routes keep to their tenant
const CONTRACT = "CBQDHNBFBZYE4MKPWBSJOPIYLW4SFSXAXUTSXJN76GNKYVYPCKWC6QUK";

type Functions = {
  kind: string;
  verified: boolean;
  functions: unknown[];
  days: { day: string; state: string | null; missing: string | null }[];
};

test("an unverified contract shows no calls per function, and the routes keep to the project", async ({
  browser,
}) => {
  const { page, ctx } = owner;
  const added = await api<{ id: string }>(ctx, "POST", `/api/tenants/${tenant.id}/onchain/ids`, {
    strkey: CONTRACT,
  });
  expect(added.status).toBe(201);
  const contractId = added.body.id;
  const view = await api<{ ids: { id: string; strkey: string; functions: unknown }[] }>(
    ctx,
    "GET",
    `/api/tenants/${tenant.id}/onchain`,
  );
  for (const id of view.body.ids) expect(id.functions).toBeNull();
  const accountId = view.body.ids.find((x) => x.strkey === ACCOUNT)?.id ?? "";

  await page.goto(`${APP}/projects/${tenant.id}/onchain`);
  // an unverified contract has no calls per function to show
  await expect(page.getByRole("region", { name: `Calls per function of ${CONTRACT}` })).toHaveCount(
    0,
  );

  const fns = await api<Functions>(
    ctx,
    "GET",
    `/api/tenants/${tenant.id}/onchain/ids/${contractId}/functions`,
  );
  expect(fns.status).toBe(200);
  expect(fns.body.kind).toBe("contract");
  expect(fns.body.verified).toBe(false);
  expect(fns.body.functions).toEqual([]);
  expect(fns.body.days).toHaveLength(30);
  for (const d of fns.body.days) {
    expect(d.state).toBeNull();
    expect(d.missing).toBe("not_verified");
  }
  const acct = await api<Functions>(
    ctx,
    "GET",
    `/api/tenants/${tenant.id}/onchain/ids/${accountId}/functions`,
  );
  expect(acct.body.kind).toBe("account");
  expect(acct.body.days[0]?.missing).toBe("not_a_contract");

  const csv = await ctx.request.get(
    `${APP}/api/tenants/${tenant.id}/onchain/ids/${contractId}/functions.csv`,
  );
  expect(csv.status()).toBe(200);
  expect(csv.headers()["content-type"]).toBe("text/csv; charset=utf-8");
  expect((await csv.text()).trim().split("\n")).toEqual([
    "day,strkey,function,caller,day_state,day_missing,successful,failed,accounts",
  ]);

  // another project's member gets 404 on both routes
  const intruder = await browser.newContext();
  try {
    await signIn(await intruder.newPage(), newUser("onchain-fn-intruder"));
    for (const path of [
      `/api/tenants/${tenant.id}/onchain/ids/${contractId}/functions`,
      `/api/tenants/${tenant.id}/onchain/ids/${contractId}/functions.csv`,
    ]) {
      const r = await api(intruder, "GET", path);
      expect(r.status, path).toBe(404);
      expect(JSON.stringify(r.body)).not.toContain(CONTRACT);
    }
  } finally {
    await intruder.close();
  }

  // an operator reads the same answer and its export
  const op = await browser.newContext();
  try {
    const opPage = await op.newPage();
    await signIn(opPage, OPERATOR);
    await operatorStepUp(opPage, OPERATOR);
    const r = await api<Functions>(
      op,
      "GET",
      `/api/operator/tenants/${tenant.id}/onchain/ids/${contractId}/functions`,
    );
    expect(r.status).toBe(200);
    expect(r.body).toEqual(fns.body);
    const file = await op.request.get(
      `${APP}/api/operator/tenants/${tenant.id}/onchain/ids/${contractId}/functions.csv`,
    );
    expect(file.status()).toBe(200);
    await opPage.goto(`${APP}/operator/projects/${tenant.id}`);
    await opPage.getByText("On-chain IDs", { exact: true }).click();
    await expect(
      opPage.getByRole("region", { name: `Calls per function of ${CONTRACT}` }),
    ).toHaveCount(0);
  } finally {
    await op.close();
  }
});
