import { randomUUID } from "node:crypto";
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, type Page, test } from "@playwright/test";

// what a tenant sees in the umami ui: a user with global role
// view-only and team role team-view-only, entering through /sso. the test
// provisions its own team, website and user through the api and removes them.

const base = (process.env.UMAMI_IT_URL ?? "").replace(/\/$/, "");
const adminPassword = process.env.UMAMI_ADMIN_PASSWORD ?? "";
const here = dirname(fileURLToPath(import.meta.url));
// screenshots are research output, kept in the gitignored scratch area
const shots = resolve(here, "../../../.scratch/scratch/umami-ui");
const chromeUA =
  "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36";

async function api<T>(method: string, path: string, token: string | null, body?: unknown) {
  const headers: Record<string, string> = { "content-type": "application/json" };
  if (token) headers.authorization = `Bearer ${token}`;
  const res = await fetch(base + path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await res.text();
  if (!res.ok) throw new Error(`${method} ${path}: ${res.status} ${text}`);
  return JSON.parse(text) as T;
}

async function login(username: string, password: string) {
  const out = await api<{ token: string }>("POST", "/api/auth/login", null, {
    username,
    password,
  });
  return out.token;
}

// the same call the replayer makes: constant test-net address, country header,
// timestamp in seconds, visitor token as salt
async function send(
  website: string,
  url: string,
  ts: number,
  salt: string,
  cache?: string,
): Promise<string> {
  const headers: Record<string, string> = {
    "content-type": "application/json",
    "user-agent": chromeUA,
    "x-edge-client-ip": "192.0.2.1",
    "cf-ipcountry": "FR",
  };
  if (cache) headers["x-umami-cache"] = cache;
  const res = await fetch(`${base}/api/send`, {
    method: "POST",
    headers,
    body: JSON.stringify({
      type: "event",
      payload: {
        website,
        hostname: "compat.example",
        url,
        referrer: "https://ref-compat.example/from",
        title: "compat",
        screen: "2560x1440",
        language: "en-US",
        timestamp: ts,
        salt,
      },
    }),
  });
  const out = (await res.json()) as { cache?: string };
  if (!res.ok || !out.cache) throw new Error(`send: ${res.status} ${JSON.stringify(out)}`);
  return out.cache;
}

type Fixture = {
  adminToken: string;
  teamId: string;
  websiteId: string;
  userId: string;
  username: string;
  password: string;
};

async function provision(): Promise<Fixture> {
  const adminToken = await login("admin", adminPassword);
  const [team] = await api<[{ id: string }]>("POST", "/api/teams", adminToken, {
    name: `compat-ui-${randomUUID().slice(0, 8)}`,
  });
  const websiteId = randomUUID();
  await api("POST", "/api/websites", adminToken, {
    id: websiteId,
    name: "compat ui",
    domain: "compat.example",
    teamId: team.id,
  });
  const username = `compat-ui-${randomUUID().slice(0, 8)}`;
  const password = randomUUID();
  const user = await api<{ id: string }>("POST", "/api/users", adminToken, {
    username,
    password,
    role: "view-only",
  });
  await api("POST", `/api/teams/${team.id}/users`, adminToken, {
    userId: user.id,
    role: "team-view-only",
  });

  // one visitor with two pages a minute apart, one visitor with one page, so
  // bounce rate and visit duration have values
  const now = Math.floor(Date.now() / 1000);
  const c = await send(websiteId, "/one", now - 300, "ui-a");
  await send(websiteId, "/two", now - 240, "ui-a", c);
  await send(websiteId, "/one", now - 200, "ui-b");
  return { adminToken, teamId: team.id, websiteId, userId: user.id, username, password };
}

async function teardown(f: Fixture) {
  await api("DELETE", `/api/users/${f.userId}`, f.adminToken);
  await api("DELETE", `/api/teams/${f.teamId}`, f.adminToken);
}

async function shot(page: Page, name: string) {
  await page.screenshot({ path: `${shots}/${name}.png`, fullPage: true });
}

// the seven rfp metrics as the overview page shows them: page views, unique
// visitors, bounce rate, visit duration in the metrics bar; referrers,
// countries and devices/browsers in the panels
async function expectSevenMetrics(page: Page, name: string) {
  for (const label of ["Visitors", "Visits", "Views", "Bounce rate", "Visit duration"]) {
    await expect(page.getByText(label, { exact: true }).first()).toBeVisible();
  }
  // the bar values: 2 visitors, 2 visits, 3 views, 50% bounce, 30s duration
  await expect(page.getByText("50%", { exact: true }).first()).toBeVisible();
  await expect(page.getByText("30s", { exact: true }).first()).toBeVisible();
  await shot(page, `${name}-top`);
  // the panels load their tables on their own, after the metrics bar
  for (const [heading, value] of [
    ["Sources", "ref-compat.example"],
    ["Location", "France"],
    ["Environment", "Chrome"],
  ] as const) {
    await page.getByRole("heading", { name: heading }).scrollIntoViewIfNeeded();
    await expect(page.getByText(value, { exact: true }).first()).toBeVisible({ timeout: 20_000 });
  }
  await page.getByRole("tab", { name: "Devices" }).click();
  await expect(page.getByText("Desktop", { exact: true }).first()).toBeVisible({ timeout: 20_000 });
  await page.getByRole("tab", { name: "OS" }).click();
  await expect(page.getByText("Linux", { exact: true }).first()).toBeVisible({ timeout: 20_000 });
}

test("view-only team member through sso", async ({ page }) => {
  mkdirSync(shots, { recursive: true });
  const f = await provision();
  const notes: Record<string, unknown> = {};
  // every api call the ui makes for this role, so the deny list can be
  // checked against what the dashboard really needs
  const calls = new Set<string>();
  page.on("request", (r) => {
    const u = new URL(r.url());
    if (u.origin === new URL(base).origin && u.pathname.startsWith("/api/")) {
      calls.add(`${r.method()} ${u.pathname.replace(/[0-9a-f]{8}-[0-9a-f-]{27}/g, "{id}")}`);
    }
  });
  try {
    const token = await login(f.username, f.password);

    // start on a same-origin page, so history has a known entry before /sso
    await page.goto("/api/heartbeat");
    const before = await page.evaluate(() => history.length);
    await page.goto(
      `/sso?token=${encodeURIComponent(token)}&url=${encodeURIComponent(`/websites/${f.websiteId}`)}`,
    );
    await page.waitForURL(`**/websites/${f.websiteId}`);
    notes.historyLengthBeforeSso = before;
    notes.historyLengthAfterSso = await page.evaluate(() => history.length);
    await expectSevenMetrics(page, "01-websites-id-overview");
    await shot(page, "01-websites-id-overview");

    // does /sso stay in history? the navigation api lists the same-origin
    // entries of this tab. umami's sso page calls router.push, not replace
    // (src/app/sso/SSOPage.tsx), so the entry with the token is expected to stay.
    const entries = await page.evaluate(() =>
      // biome-ignore lint/suspicious/noExplicitAny: the navigation api is not in the dom lib of typescript 7
      ((globalThis as any).navigation.entries() as { url: string }[]).map((e) => e.url),
    );
    notes.historyEntries = entries.map((u) => u.replace(encodeURIComponent(token), "<token>"));
    const ssoKept = entries.some((u) => u.includes("/sso?token="));
    notes.ssoEntryKeptInHistory = ssoKept;
    expect(ssoKept, "umami's /sso entry stays in history; the token is in it").toBe(true);
    // going back lands on /sso, which pushes the target again
    await page.goBack();
    await page.waitForTimeout(1500);
    notes.urlAfterBackSettled = page.url().replace(encodeURIComponent(token), "<token>");

    // team-scoped path, which umami's own navigation uses inside a team
    await page.goto(`/teams/${f.teamId}/websites/${f.websiteId}`);
    await expectSevenMetrics(page, "02-teams-id-websites-id-overview");
    await shot(page, "02-teams-id-websites-id-overview");

    // what else the role can open. each page is recorded, not asserted, except
    // that nothing offers a way to change the website
    const pages: Record<string, string> = {
      websites: "/websites",
      teamWebsites: `/teams/${f.teamId}/websites`,
      sessions: `/websites/${f.websiteId}/sessions`,
      events: `/websites/${f.websiteId}/events`,
      realtime: `/websites/${f.websiteId}/realtime`,
      compare: `/websites/${f.websiteId}/compare`,
      websiteSettings: `/websites/${f.websiteId}/settings`,
      settingsWebsite: `/settings/websites/${f.websiteId}`,
      settingsTeams: "/settings/teams",
      teamSettings: `/settings/teams/${f.teamId}`,
      admin: "/admin/users",
    };
    const seen: Record<string, { url: string; text: string }> = {};
    let i = 3;
    for (const [name, path] of Object.entries(pages)) {
      await page.goto(path);
      await page.waitForLoadState("load");
      await page.waitForTimeout(2000);
      const text = await page.locator("body").innerText({ timeout: 10_000 });
      seen[name] = {
        url: new URL(page.url()).pathname,
        text: text.replace(/\s+/g, " ").slice(0, 600),
      };
      await shot(page, `${String(i++).padStart(2, "0")}-${name}`);
    }
    notes.pages = seen;

    // umami renders the website settings page, with its edit form and buttons,
    // to this role too; only the api refuses. record the buttons, then prove
    // that saving a new name through the ui changes nothing.
    await page.goto(`/websites/${f.websiteId}/settings`);
    await expect(page.getByLabel("Name")).toBeVisible();
    notes.settingsButtons = await page.getByRole("button").allInnerTexts();
    await page.getByLabel("Name").fill("changed by viewer");
    await page.getByRole("button", { name: "Save", exact: true }).click();
    await page.waitForTimeout(2000);
    await shot(page, "99-settings-save-attempt");
    const site = await api<{ name: string }>("GET", `/api/websites/${f.websiteId}`, f.adminToken);
    expect(site.name).toBe("compat ui");
  } finally {
    notes.apiCalls = [...calls].sort();
    writeFileSync(`${shots}/notes.json`, `${JSON.stringify(notes, null, 2)}\n`);
    await teardown(f);
  }
});
