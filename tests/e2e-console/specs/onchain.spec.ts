// the on-chain witness, "what scf sees" and the csv exports, end to end. an
// owner declares an account in the console and sees it unverified with a
// challenge to prove it; the scf view and the overview export answer for
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
import { api, enrollApi, type GitHubUser, newUser, signIn, type Tenant } from "./helpers.ts";
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
  await expect(page.getByRole("heading", { name: "Accounts and contracts" })).toBeVisible();
  await page.getByLabel("Account or contract").fill("not-a-key");
  await page.getByRole("button", { name: "Add", exact: true }).click();
  await expect(
    page.getByText("Enter a Stellar account (G…) or a Soroban contract (C…)"),
  ).toBeVisible();

  await page.getByLabel("Account or contract").fill(ACCOUNT);
  await page.getByRole("button", { name: "Add", exact: true }).click();
  await expect(page.getByText(ACCOUNT, { exact: true })).toBeVisible();
  await expect(page.getByText("Unverified").first()).toBeVisible();

  await page.getByRole("button", { name: "Prove control" }).click();
  const message = page.locator("pre", { hasText: "Veristar ownership proof" });
  await expect(message).toContainText(`Project: ${tenant.id}`);
  await expect(message).toContainText(`Account: ${ACCOUNT}`);
  await expect(page.getByText("Or publish a data entry")).toBeVisible();

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

  // the overview shows the on-chain panel next to web and api
  await page.goto(`${APP}/projects/${tenant.id}`);
  await expect(page.getByRole("heading", { name: "On-chain" })).toBeVisible();
  await expect(page.getByText("0 of 1")).toBeVisible();
});

test("what scf sees and the overview export answer for the project", async () => {
  const { page, ctx } = owner;
  await page.goto(`${APP}/projects/${tenant.id}/scf-view`);
  await expect(page.getByRole("heading", { name: "What SCF sees" })).toBeVisible();
  await expect(page.getByText("The project's on-chain column.")).toBeVisible();
  const href = await page.getByRole("link", { name: "Export CSV" }).getAttribute("href");
  expect(href).toMatch(new RegExp(`^/api/tenants/${tenant.id}/overview\\.csv\\?from=`));

  const r = await ctx.request.get(`${APP}${href}`);
  expect(r.status()).toBe(200);
  expect(r.headers()["content-type"]).toBe("text/csv; charset=utf-8");
  expect(r.headers()["content-disposition"]).toContain("attachment");
  const lines = (await r.text()).trim().split("\n");
  const header = lines[0]?.split(",") ?? [];
  expect(header.slice(0, 13)).toEqual([
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
    await page.goto(`${APP}/operator/web?q=${encodeURIComponent(`On-chain ${run}`)}`);
    await expect(page.getByRole("columnheader", { name: "On-chain" })).toBeVisible();
    const row = page.locator(`tr[data-tenant="${tenant.id}"]`);
    await expect(row.locator("[data-col=onchain]")).toContainText("0/1 verified");

    const href = await page.getByRole("link", { name: "Export CSV" }).getAttribute("href");
    expect(href).toContain("/api/operator/web/projects.csv?");
    expect(href).toContain("rows=1");
    const r = await ctx.request.get(`${APP}${href}`);
    expect(r.status()).toBe(200);
    const lines = (await r.text()).trim().split("\n");
    expect(lines).toHaveLength(2);
    expect(lines[0]).toContain("onchain_operations");
    expect(lines[1]?.startsWith(tenant.id)).toBe(true);

    // the export wrote an audit row with the query it answered
    const audit = await api<{ items: { action: string; detail: { view?: string } }[] }>(
      ctx,
      "GET",
      "/api/operator/audit?action=export.csv",
    );
    expect(audit.status).toBe(200);
    expect(audit.body.items.some((e) => e.detail.view === "web_projects")).toBe(true);

    await page.goto(`${APP}/operator/tenants/${tenant.id}`);
    const section = page.locator("section", {
      has: page.getByRole("heading", { name: "On-chain" }),
    });
    await expect(section).toContainText(ACCOUNT);
    await expect(section).toContainText("Unverified");
  } finally {
    await ctx.close();
  }
});
