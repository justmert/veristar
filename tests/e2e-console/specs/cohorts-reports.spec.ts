// cohorts, threshold rules and signed usage reports, end to end
// (control-plane-api sections 19 and 20). a project owner proposes a cohort
// on "what scf sees"; an operator confirms it on the project's page, filters
// the global web view by it, saves a threshold rule and opens its matches.
// the owner then generates a signed report, and a browser without any
// session opens its public link, where the page checks the sep-53 signature
// itself, and downloads the json. after the owner revokes the link the same
// address shows that the report is not available.
import { readFileSync } from "node:fs";
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
// the cohort carries a run suffix, so the filter isolates this run
const run = `c${Math.random().toString(36).slice(2, 7)}`;
const PROGRAM = `Build Award ${run}`;

let op: { ctx: BrowserContext; page: Page };
let owner: { ctx: BrowserContext; page: Page };
let tenant: Tenant;
const problems: string[] = [];

function watch(page: Page) {
  page.on("pageerror", (e) => problems.push(`pageerror: ${e.message}`));
  page.on("console", (m) => {
    if (m.type() === "error") problems.push(`console: ${m.text()}`);
  });
}

// the closed utc days of a report: the week ending yesterday
// a week of final days: a report ends four days before today at the latest
function lastWeek(): { from: string; to: string } {
  const day = (offset: number) =>
    new Date(Date.now() - offset * 86_400_000).toISOString().slice(0, 10);
  return { from: day(10), to: day(4) };
}

test.beforeAll(async ({ browser }) => {
  const octx = await browser.newContext();
  op = { ctx: octx, page: await octx.newPage() };
  watch(op.page);
  await signIn(op.page, OPERATOR);
  await operatorStepUp(op.page, OPERATOR);
  const tctx = await browser.newContext();
  owner = { ctx: tctx, page: await tctx.newPage() };
  watch(owner.page);
  await signIn(owner.page, newUser("cohortown"));
  tenant = await enrollApi(owner.ctx, `Cohort ${run}`);
  expect(tenant.status, "the stack enrolls in open mode").toBe("active");
});

test.afterAll(async () => {
  await op?.ctx.close();
  await owner?.ctx.close();
});

test("the owner proposes a cohort and an operator confirms it", async () => {
  const page = owner.page;
  await page.goto(`${APP}/projects/${tenant.id}/scf`);
  // the cohort is on demand, under the verdict
  await page.locator("details#cohort > summary").click();
  const panel = page.getByRole("region", { name: "SCF cohort" });
  await expect(panel).toContainText("No cohort is confirmed for your project yet.");
  await panel.getByRole("button", { name: "Propose a cohort" }).click();
  await panel.getByLabel("SCF program").fill(PROGRAM);
  await panel.getByLabel("Round").fill("SCF #38");
  await panel.getByLabel("Award date").fill(lastWeek().from);
  await panel.getByRole("button", { name: "Send proposal" }).click();
  await expect(panel.locator("[data-proposal]")).toContainText(PROGRAM);

  const o = op.page;
  await o.goto(`${APP}/operator/projects/${tenant.id}`);
  const cohort = o.getByRole("region", { name: "SCF cohort" });
  await expect(cohort.locator("[data-proposal]")).toContainText("SCF #38");
  await cohort.getByRole("button", { name: "Confirm proposal" }).click();
  await expect(cohort.locator("[data-proposal]")).toHaveCount(0);
  await expect(cohort).toContainText(PROGRAM);

  // the confirmation is in the audit log of the project
  const audit = await api<{ items: { action: string }[] }>(
    op.ctx,
    "GET",
    `/api/operator/audit?tenant_id=${tenant.id}&limit=50`,
  );
  expect(audit.status).toBe(200);
  expect(audit.body.items.map((i) => i.action)).toEqual(
    expect.arrayContaining(["cohort.propose", "cohort.confirm"]),
  );

  // the global web view filters by the confirmed program and shows it
  await o.goto(`${APP}/operator/projects?view=web`);
  await o.getByRole("button", { name: /^Filters/ }).click();
  const sheet = o.getByRole("dialog", { name: "Filters" });
  await sheet.getByLabel("SCF program").selectOption(PROGRAM);
  await expect(o).toHaveURL(
    new RegExp(`program=${encodeURIComponent(PROGRAM).replaceAll("%20", "(\\+|%20)")}`),
  );
  await sheet.getByRole("button", { name: "Show results" }).click();
  // the cohort is an optional column
  await o.getByRole("button", { name: "Columns" }).click();
  await o.getByRole("menuitemcheckbox", { name: "Cohort" }).click();
  await o.keyboard.press("Escape");
  const row = o.locator(`tr[data-tenant="${tenant.id}"]`);
  await expect(row.locator("[data-cohort]")).toContainText(`${PROGRAM}, SCF #38`);
  await expect(o.locator("tbody tr")).toHaveCount(1);
});

test("an operator saves a threshold rule and opens its matches", async () => {
  const o = op.page;
  await o.goto(`${APP}/operator/rules`);
  await o.getByRole("button", { name: "New rule" }).click();
  // the form opens in a dialog
  const form = o.getByRole("dialog", { name: "New rule" });
  await form.getByLabel("Name").fill(`Steady web ${run}`);
  await form.getByLabel("Minimum per day").fill("1");
  await form.getByLabel("Days per month").fill("1");
  await form.getByLabel("Complete months").fill("1");
  // the cohort is chosen from the confirmed values
  await form.getByLabel("SCF program").selectOption(PROGRAM);
  await form.getByRole("button", { name: "Save rule" }).click();
  const matches = o.getByRole("region", { name: "Rule matches" });
  // the project has no traffic, so it is evaluated and does not qualify
  await expect(matches).toContainText("0 of 1 project meet the rule");
  // the list of rules says how many projects meet each one
  await expect(
    o.getByRole("table", { name: "Rules" }).locator("tr", { hasText: `Steady web ${run}` }),
  ).toContainText("0 of 1");
  const csv = matches.getByRole("link", { name: "Export CSV" });
  await expect(csv).toHaveAttribute("href", /\/api\/operator\/rules\/[0-9a-f-]{36}\/matches\.csv$/);

  // the rule reaches the machine-readable list too
  const rules = await api<{ items: { name: string }[] }>(op.ctx, "GET", "/api/operator/rules");
  expect(rules.body.items.map((r) => r.name)).toContain(`Steady web ${run}`);
});

test("a signed report verifies in a browser without a session, until its link is revoked", async ({
  browser,
}) => {
  const page = owner.page;
  const period = lastWeek();
  await page.goto(`${APP}/projects/${tenant.id}/scf`);
  await page.getByRole("button", { name: "Sign a report" }).click();
  const gen = page.getByRole("dialog", { name: "Sign a report" });
  await gen.getByLabel("From").fill(period.from);
  await gen.getByLabel("To").fill(period.to);
  await gen.getByRole("button", { name: "Generate and sign" }).click();
  // the link shows in the same dialog, now titled for the signed report
  const signed = page.getByRole("dialog", { name: "Report signed" });
  const link = signed.getByRole("status").getByRole("link");
  await expect(link).toHaveAttribute("href", new RegExp(`^${APP}/r/[a-z2-7]{26}$`));
  const url = (await link.getAttribute("href")) ?? "";

  // a reader with no account: a fresh context, no cookie
  const reader = await browser.newContext({
    proxy: { server: test.info().project.use.proxy?.server ?? "" },
    ignoreHTTPSErrors: true,
    acceptDownloads: true,
  });
  try {
    const r = await reader.newPage();
    watch(r);
    await r.goto(url);
    await expect(r.locator("[data-verdict=verified]")).toHaveText(
      "Signed by Veristar. This browser checked the signature.",
    );
    await r.getByText("Signature details").click();
    await expect(r.getByRole("region", { name: "Signature" })).toContainText(
      "Veristar's current report key",
    );
    await expect(r.locator("h1")).toHaveText(`Cohort ${run}`);
    await expect(r.getByRole("region", { name: "Cohort" })).toContainText(PROGRAM);

    const [download] = await Promise.all([
      r.waitForEvent("download"),
      r.getByRole("button", { name: "Download JSON" }).click(),
    ]);
    // saveAs, not path(): with the browsers in their own container
    // (tests/fixtures/browserbox) the download lives there, and saveAs
    // streams it to this machine
    const saved = test.info().outputPath("signed-report.json");
    await download.saveAs(saved);
    const file = JSON.parse(readFileSync(saved, "utf8")) as {
      report: { project: { id: string }; period: { from: string; to: string } };
      signature: { scheme: string; signer: string };
    };
    expect(file.signature.scheme).toBe("sep-53");
    expect(file.report.project.id).toBe(tenant.id);
    expect(file.report.period).toMatchObject(period);

    // the signer is the key the console host's stellar.toml publishes
    const toml = await reader.request.get(`${APP}/.well-known/stellar.toml`);
    expect(toml.status()).toBe(200);
    expect(toml.headers()["access-control-allow-origin"]).toBe("*");
    expect(await toml.text()).toContain(file.signature.signer);

    // the owner revokes the link; the same address now says so
    await page.reload();
    const reports = page.getByRole("list", { name: "Reports" });
    await reports
      .getByRole("button", { name: /^Actions for the report for / })
      .first()
      .click();
    await page.getByRole("menuitem", { name: "Revoke link" }).click();
    await page.getByRole("dialog").getByRole("button", { name: "Revoke link" }).click();
    await expect(reports.getByText("Link revoked").first()).toBeVisible();

    const again = await reader.newPage();
    await again.goto(url);
    await expect(again.getByText("This report link is not available")).toBeVisible();
  } finally {
    await reader.close();
  }
  expect(problems.filter((p) => !p.includes("404") && !p.includes("410"))).toEqual([]);
});
