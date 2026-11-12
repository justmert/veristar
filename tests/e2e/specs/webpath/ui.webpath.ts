// a tenant's dashboard session in the umami ui, for the egress check of the
// this stage web path suite. the go test captures every packet of the umami
// container meanwhile; this side records every request the page makes to a
// host other than umami, which must stay empty as well.
import { writeFileSync } from "node:fs";
import { join } from "node:path";
import { expect, test } from "@playwright/test";

function env(name: string): string {
  const v = process.env[name];
  if (v === undefined || v === "") throw new Error(`missing env ${name}`);
  return v;
}

test("dashboard session of a view-only member @ui", async ({ page }) => {
  const base = env("WEBPATH_UMAMI_URL").replace(/\/$/, "");
  const token = env("WEBPATH_UMAMI_TOKEN");
  const website = env("WEBPATH_WEBSITE");
  const team = env("WEBPATH_TEAM");
  const origin = new URL(base).origin;
  const foreign: string[] = [];
  const visited: string[] = [];
  let requests = 0;
  page.on("request", (r) => {
    requests++;
    const u = new URL(r.url());
    if ((u.protocol === "http:" || u.protocol === "https:") && u.origin !== origin) {
      foreign.push(r.url());
    }
  });

  await page.goto(
    `${base}/sso?token=${encodeURIComponent(token)}&url=${encodeURIComponent(`/websites/${website}`)}`,
  );
  await page.waitForURL(`**/websites/${website}`);
  for (const label of ["Visitors", "Visits", "Views", "Bounce rate", "Visit duration"]) {
    await expect(page.getByText(label, { exact: true }).first()).toBeVisible();
  }
  // every page a tenant can open for its website and team; the referrer and
  // location panels are where umami would load favicons and flags
  const paths = [
    `/websites/${website}`,
    `/websites/${website}/realtime`,
    `/websites/${website}/sessions`,
    `/websites/${website}/events`,
    `/websites/${website}/compare`,
    `/websites/${website}/breakdown`,
    `/websites/${website}/settings`,
    `/teams/${team}/websites`,
    `/settings/teams/${team}`,
  ];
  for (const p of paths) {
    await page.goto(`${base}${p}`);
    await page.waitForLoadState("networkidle");
    visited.push(p);
  }
  await page.goto(`${base}/websites/${website}`);
  for (const heading of ["Sources", "Location", "Environment"]) {
    await page.getByRole("heading", { name: heading }).scrollIntoViewIfNeeded();
  }
  await page.waitForLoadState("networkidle");
  await page.screenshot({ path: join(env("WEBPATH_OUT"), "ui-overview.png"), fullPage: true });
  writeFileSync(
    join(env("WEBPATH_OUT"), "ui.json"),
    `${JSON.stringify({ visited, requests, foreign }, null, 2)}\n`,
  );
  expect(foreign, "requests from the dashboard to hosts other than umami").toEqual([]);
});
