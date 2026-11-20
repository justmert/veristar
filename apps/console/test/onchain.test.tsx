import { Outlet } from "react-router";
import { describe, expect, it } from "vitest";
import { contractFunctions, exportPaths, onchainView } from "@/api/onchain";
import { webProject } from "@/api/schemas";
import { WeeklyActiveList } from "@/components/onchain";
import { ContractFunctionsBlock, FunctionsDaily } from "@/components/onchain-functions";
import { EMPTY_FILTERS } from "@/lib/global";
import { chainGap, NO_LEDGER_READ, NO_VERIFIED_IDS } from "@/lib/onchain";
import { listExportHref } from "@/lib/operator-projects";
import { OnchainValue } from "@/pages/operator/shared";
import { OnchainPage } from "@/pages/project/onchain";
import { ScfReviewPage } from "@/pages/project/scf";
import type { ProjectContext } from "@/pages/project-layout";
import * as f from "./fixtures/contract";
import {
  byLabel,
  byRole,
  click,
  pickRowAction,
  render,
  renderRoutes,
  routeFetch,
  stubFetch,
  type,
  waitFor,
} from "./helpers";

const ACCOUNT_ID = "0193a6f8-6666-7a88-9b99-caadbbeccddd";
const CONTRACT_ID = "0193a6f8-7777-7b99-acaa-dbbeccddeeff";
const days = ["2026-11-13", "2026-11-14", "2026-11-15", "2026-11-16"];

const none = {
  status: "unverified",
  method: null,
  verified_at: null,
  via: null,
  detail: null,
  last_attempt_at: null,
  last_error: null,
};

// the view of contract 15.3: a verified account read on two days, one of
// them a zero, and a contract whose reads fail
const viewBody = {
  from: days[0],
  to: days[3],
  days,
  sources: { accounts: "horizon", contracts: "rpc_events", account_backfill_days: 90 },
  totals: {
    ids: 2,
    ids_verified: 1,
    accounts: 1,
    contracts: 1,
    operations: 12,
    transactions: 5,
    days_missing: 4,
    weekly_active_accounts: { average: 42, latest: 40, weeks_counted: 2, weeks_missing: 1 },
  },
  ids: [
    {
      id: ACCOUNT_ID,
      strkey: f.G_KEY,
      kind: "account",
      added_by: "enrollment",
      created_at: "2026-11-12T09:00:00Z",
      verification: {
        ...none,
        status: "verified",
        method: "sep53",
        verified_at: "2026-11-12T10:00:00Z",
        detail: { standard: "sep-53" },
      },
      read: {
        source: "horizon",
        covered_from: days[1],
        covered_to: days[2],
        last_success_at: "2026-11-16T01:00:00Z",
        last_error: null,
        last_error_at: null,
        home_domain: "example.org",
        account_exists: true,
      },
      totals: {
        operations: 12,
        transactions: 5,
        peak_counterparties: 3,
        days_read: 2,
        days_missing: 1,
      },
      series: [
        {
          day: days[0],
          operations: null,
          transactions: null,
          counterparties: null,
          missing: "before_backfill_window",
        },
        { day: days[1], operations: 12, transactions: 5, counterparties: 3, missing: null },
        { day: days[2], operations: 0, transactions: 0, counterparties: 0, missing: null },
        {
          day: days[3],
          operations: null,
          transactions: null,
          counterparties: null,
          missing: "day_not_complete",
        },
      ],
      functions: null,
    },
    {
      id: CONTRACT_ID,
      strkey: f.C_KEY,
      kind: "contract",
      added_by: "console",
      created_at: "2026-11-13T09:00:00Z",
      verification: {
        ...none,
        last_attempt_at: "2026-11-14T09:00:00Z",
        last_error: "no_admin_entry",
      },
      read: {
        source: "rpc_events",
        covered_from: null,
        covered_to: null,
        last_success_at: null,
        last_error: "rate_limited",
        last_error_at: "2026-11-16T01:00:00Z",
        home_domain: null,
        account_exists: null,
      },
      totals: {
        operations: 0,
        transactions: 0,
        peak_counterparties: 0,
        days_read: 0,
        days_missing: 3,
      },
      series: days.map((d, i) => ({
        day: d,
        operations: null,
        transactions: null,
        counterparties: null,
        missing: i === 3 ? "day_not_complete" : "rate_limited",
      })),
      functions: null,
    },
  ],
  weeks: [
    { week: "2026-W44", from: "2026-10-26", to: "2026-11-01", active_accounts: 44, missing: null },
    {
      week: "2026-W45",
      from: "2026-11-02",
      to: "2026-11-08",
      active_accounts: null,
      missing: "day_over_read_cap",
    },
    { week: "2026-W46", from: "2026-11-09", to: "2026-11-15", active_accounts: 40, missing: null },
  ],
  link: {
    two_way: true,
    sites: [
      {
        site_id: f.SITE_ID,
        domain: "example.org",
        verified: true,
        stellar_toml: "found",
        org_url: "https://example.org",
        org_url_matches: true,
        accounts: [
          {
            strkey: f.G_KEY,
            listed_in_toml: true,
            home_domain: "example.org",
            home_domain_matches: true,
            two_way: true,
          },
        ],
        contracts: [{ strkey: f.C_KEY, listed_in_toml: false }],
        two_way: true,
      },
    ],
  },
};

const ctx: ProjectContext = {
  tenant: f.tenantBody as ProjectContext["tenant"],
  me: f.meBody as ProjectContext["me"],
  isOwner: true,
  termsCurrent: true,
};

function routes(page: React.ReactNode, path: string) {
  return [
    {
      path: "/projects/:tenantId",
      element: <Outlet context={ctx} />,
      children: [{ path, element: page }],
    },
  ];
}

// the view of every on-chain read: the range asked for, and the previous
// range of the same length that the changes are read against
function chainFetch(body: unknown = viewBody, more?: (method: string, url: string) => unknown) {
  return routeFetch((method, url) => {
    const extra = more?.(method, url);
    if (extra) return extra as { status: number; body?: unknown };
    return { status: 200, body };
  });
}

describe("on-chain page activity (contract 15.3)", () => {
  const path = `/projects/${f.TENANT_ID}/onchain`;

  it("counts verified ids only and shows why days are missing", async () => {
    const calls = chainFetch();
    const host = await renderRoutes(routes(<OnchainPage />, "onchain"), path);
    await waitFor(() => expect(host.querySelector("[data-stat=operations]")).not.toBeNull());
    // the default range is the control plane's own; the previous range of
    // the same length is read for the changes
    expect(calls[0]?.url).toBe(`/api/tenants/${f.TENANT_ID}/onchain`);
    await waitFor(() =>
      expect(calls.map((c) => c.url)).toContain(
        `/api/tenants/${f.TENANT_ID}/onchain?from=2026-11-09&to=2026-11-12`,
      ),
    );
    // the unverified contract's failed reads are not part of the totals
    expect(host.querySelector("[data-stat=operations]")?.textContent).toContain("12");
    // one verified day before today has no row: the same definition as the
    // scf review
    expect(host.querySelector("[data-stat=days-missing]")?.textContent).toContain("1");
    expect(host.textContent).toContain("Verified");
    expect(host.textContent).toContain("Unverified");
    expect(host.textContent).toContain(
      "1 day without data. Before the first day read for this account",
    );
    // an unverified id draws no daily bars: nothing of it counts
    expect(host.querySelector(`[aria-label="Daily operations of ${f.C_KEY}"]`)).toBeNull();
    expect(host.querySelector(`[aria-label="Daily operations of ${f.G_KEY}"]`)).not.toBeNull();
    expect(byRole(host, "button", "Prove control")).not.toBeNull();
    const link = [...host.querySelectorAll("a")].find((a) => a.textContent?.includes("Export CSV"));
    expect(link?.getAttribute("href")).toBe(
      `/api/tenants/${f.TENANT_ID}/overview.csv?from=${days[0]}&to=${days[3]}`,
    );
  });

  it("reads a chosen range from the address", async () => {
    const calls = chainFetch();
    await renderRoutes(
      routes(<OnchainPage />, "onchain"),
      `${path}?range=custom&from=2026-10-01&to=2026-10-31`,
    );
    await waitFor(() => expect(calls.length).toBeGreaterThan(0));
    expect(calls[0]?.url).toBe(`/api/tenants/${f.TENANT_ID}/onchain?from=2026-10-01&to=2026-10-31`);
  });

  it("shows weekly active accounts per iso week, a missing week with its reason", async () => {
    chainFetch();
    const host = await renderRoutes(routes(<OnchainPage />, "onchain"), path);
    await waitFor(() => expect(host.querySelector("[data-testid=waa-summary]")).not.toBeNull());
    expect(host.querySelector("[data-testid=waa-summary]")?.textContent).toBe(
      "42 a week on average over 2 weeks, 1 missing",
    );
    const weeks = host.querySelectorAll("[data-week]");
    expect(weeks).toHaveLength(3);
    expect(weeks[0]?.textContent).toContain("44");
    // a week that could not be counted whole is never shown as zero
    const missing = host.querySelector('[data-week="2026-W45"]');
    expect(missing?.querySelector("[data-missing]")).not.toBeNull();
    expect(missing?.textContent).toContain("More activity than one run reads");
    expect(missing?.textContent).not.toMatch(/\b0\b/);
    // selecting the figure draws the weeks in the chart
    await click(host.querySelector<HTMLElement>("[data-stat=waa]") as HTMLElement);
    expect(host.textContent).toContain("Weekly active accounts per ISO week");
  });

  it("names the next step of the stellar.toml link, with one action for it", async () => {
    const body = {
      ...viewBody,
      link: {
        two_way: false,
        sites: [
          {
            ...viewBody.link.sites[0],
            org_url: "https://other.org",
            org_url_matches: false,
            two_way: false,
            accounts: [
              {
                strkey: f.G_KEY,
                listed_in_toml: false,
                home_domain: null,
                home_domain_matches: false,
                two_way: false,
              },
            ],
          },
        ],
      },
    };
    chainFetch(body);
    const host = await renderRoutes(routes(<OnchainPage />, "onchain"), path);
    await waitFor(() => expect(host.querySelector("[data-testid=link-count]")).not.toBeNull());
    expect(host.querySelector("[data-testid=link-count]")?.textContent).toBe(
      "0 of 1 account linked both ways",
    );
    const row = host.querySelector(`[data-link-row="${f.SITE_ID}|${f.G_KEY}"]`);
    // the domain is verified; the file does not name it yet
    expect(row?.querySelector("[data-link-state]")?.getAttribute("data-link-state")).toBe("toml");
    expect(row?.textContent).toContain("Name the domain in stellar.toml");
    expect(row?.textContent).toContain("1 of 4");
    expect(row?.textContent).not.toContain("ORG_URL");
    expect(
      row?.querySelector('[aria-label^="Copy the lines that name example.org"]'),
    ).not.toBeNull();
  });

  it("is one card to add an id until one is added", async () => {
    chainFetch({
      ...viewBody,
      ids: [],
      totals: { ...viewBody.totals, ids: 0, ids_verified: 0, accounts: 0, contracts: 0 },
      link: { two_way: false, sites: [] },
    });
    const host = await renderRoutes(routes(<OnchainPage />, "onchain"), path);
    await waitFor(() => expect(host.querySelector("[data-first-step]")).not.toBeNull());
    expect(host.querySelector('[data-step="add"]')).not.toBeNull();
    expect(byLabel(host, "Account or contract")).not.toBeNull();
    // no range, no empty numbers, no link and no export before a proof
    expect(host.querySelector("[data-stat]")).toBeNull();
    expect(host.textContent).not.toContain("stellar.toml");
    expect(host.textContent).not.toContain("Export CSV");
  });
});

const challengeBody = {
  id: ACCOUNT_ID,
  account: f.G_KEY,
  message: `Veristar ownership proof\nConsole: app.veristar.app\nProject: "Project" (${f.TENANT_ID})\nAccount: ${f.G_KEY}\nNonce: 00112233445566778899aabbccddeeff\nExpires: 2026-11-16T10:00:00Z`,
  manage_data: { name: "veristar", value: "00112233445566778899aabbccddeeff" },
  expires_at: "2026-11-16T10:00:00Z",
};

describe("on-chain first step", () => {
  it("shows one proof card per declared id until one is proven", async () => {
    chainFetch(
      {
        ...viewBody,
        ids: viewBody.ids.map((id) => ({ ...id, verification: none })),
        totals: { ...viewBody.totals, ids_verified: 0 },
      },
      (method, url) =>
        method === "POST" && url.endsWith("/challenge")
          ? { status: 200, body: challengeBody }
          : null,
    );
    const host = await renderRoutes(
      routes(<OnchainPage />, "onchain"),
      `/projects/${f.TENANT_ID}/onchain`,
    );
    await waitFor(() => expect(host.querySelector("[data-first-step]")).not.toBeNull());
    expect(host.querySelectorAll('[data-first-step] [data-step="prove"]')).toHaveLength(2);
    // the account's message is asked for when its card opens
    await waitFor(() => expect(host.textContent).toContain("Veristar ownership proof"));
    expect(host.textContent).toContain("Connect a wallet and sign");
    expect(host.textContent).toContain("Check the admin");
    expect(host.querySelector('[aria-label^="Daily operations"]')).toBeNull();
  });
});

describe("weekly active accounts list", () => {
  it("draws nothing when the range holds no counted week", async () => {
    const host = await render(<WeeklyActiveList weeks={[]} label="Weeks" />);
    expect(host.textContent).toBe("");
  });
});

describe("accounts and contracts page (contract 15.4)", () => {
  const path = `/projects/${f.TENANT_ID}/onchain`;

  it("adds an id and refuses a malformed one before sending", async () => {
    const calls = chainFetch(viewBody, (method) =>
      method === "POST"
        ? {
            status: 201,
            body: {
              id: "0193a6f8-8888-7caa-bdbb-eccddeeff001",
              strkey: f.G_KEY,
              kind: "account",
              added_by: "console",
              created_at: "2026-11-16T09:00:00Z",
              verification: none,
            },
          }
        : null,
    );
    const host = await renderRoutes(routes(<OnchainPage />, "onchain"), path);
    await waitFor(() => expect(host.textContent).toContain("Accounts and contracts"));
    await click(byRole(host, "button", "Add account or contract"));
    await type(byLabel(host, "Account or contract"), "not-a-key");
    await click(byRole(host, "button", "Add"));
    expect(host.textContent).toContain("Enter a Stellar account (G…) or a Soroban contract (C…)");
    expect(calls.some((c) => c.method === "POST")).toBe(false);
    await type(byLabel(host, "Account or contract"), f.G_KEY);
    await click(byRole(host, "button", "Add"));
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    const post = calls.find((c) => c.method === "POST");
    expect(post?.url).toBe(`/api/tenants/${f.TENANT_ID}/onchain/ids`);
    expect(post?.headers.get("X-Requested-With")).toBe("sa-console");
    expect(post?.body).toEqual({ strkey: f.G_KEY });
  });

  it("proves an account again with a data entry", async () => {
    const calls = chainFetch(viewBody, (method, url) =>
      method !== "POST"
        ? null
        : url.endsWith("/challenge")
          ? { status: 200, body: challengeBody }
          : {
              status: 200,
              body: {
                id: ACCOUNT_ID,
                strkey: f.G_KEY,
                kind: "account",
                added_by: "enrollment",
                created_at: "2026-11-12T09:00:00Z",
                verification: {
                  ...none,
                  status: "verified",
                  method: "manage_data",
                  verified_at: "2026-11-16T09:30:00Z",
                },
              },
            },
    );
    const host = await renderRoutes(routes(<OnchainPage />, "onchain"), path);
    await waitFor(() => expect(host.querySelector("[data-stat=operations]")).not.toBeNull());
    await pickRowAction(host, f.G_KEY, "Prove again");
    await waitFor(() => expect(host.textContent).toContain("Veristar ownership proof"));
    await click(byRole(host, "radio", "Publish a data entry"));
    expect(host.textContent).toContain("00112233445566778899aabbccddeeff");
    await click(byRole(host, "button", "Check the data entry"));
    await waitFor(() => expect(calls.filter((c) => c.method === "POST")).toHaveLength(2));
    const posts = calls.filter((c) => c.method === "POST");
    expect(posts[0]?.url).toBe(`/api/tenants/${f.TENANT_ID}/onchain/ids/${ACCOUNT_ID}/challenge`);
    expect(posts[1]?.url).toBe(`/api/tenants/${f.TENANT_ID}/onchain/ids/${ACCOUNT_ID}/verify`);
    expect(posts[1]?.body).toEqual({ method: "manage_data" });
  });

  it("checks a contract's admin and shows the last error in words", async () => {
    const calls = chainFetch(viewBody, (method) =>
      method === "POST"
        ? {
            status: 200,
            body: {
              id: CONTRACT_ID,
              strkey: f.C_KEY,
              kind: "contract",
              added_by: "console",
              created_at: "2026-11-13T09:00:00Z",
              verification: none,
            },
          }
        : null,
    );
    const host = await renderRoutes(routes(<OnchainPage />, "onchain"), path);
    await waitFor(() => expect(host.querySelector("[data-stat=operations]")).not.toBeNull());
    expect(host.querySelector('[data-last-error="no_admin_entry"]')?.textContent).toContain(
      "Last check: The contract stores no Admin or Owner entry that can be read.",
    );
    await click(byRole(host, "button", "Prove control"));
    await waitFor(() => expect(host.textContent).toContain("Check the admin"));
    await click(byRole(host, "button", "Check the admin"));
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ method: "contract_admin" });
  });
});

const scfBody = {
  from: days[0],
  to: days[3],
  days,
  in_operator_views: true,
  tenant: {
    id: f.TENANT_ID,
    slug: "stellar-dex",
    name: "Stellar DEX",
    status: "active",
    created_at: "2026-11-12T09:00:00Z",
  },
  sites: { sites: 1, sites_verified: 1, sites_with_stellar_toml: 1 },
  has_web_data: true,
  has_api_data: false,
  web: {
    pageviews: 1234,
    visitors: 300,
    visits: 400,
    bounces: 100,
    totaltime: 8000,
    bounce_rate: 0.25,
    avg_visit_duration: 20,
    quarantine_pageviews: 10,
    quarantine_visitors: 4,
    quarantine_share: 0.008,
    ingest_requests: 1300,
    ingest_rejected: 13,
    rejected_share: 0.01,
    coverage: { script_requests: 1100, gpc_requests: 99, unmeasured_share: 0.09 },
  },
  api: {
    requests: 0,
    requests_4xx: 0,
    requests_5xx: 0,
    rate_4xx: null,
    rate_5xx: null,
    p50_seconds: null,
    p50_over_10s: false,
    p95_seconds: null,
    p95_over_10s: false,
    p99_seconds: null,
    p99_over_10s: false,
    infrastructure_requests: 0,
    services: 0,
    routes: 0,
    source: null,
    lossy: false,
  },
  onchain: {
    ids: 2,
    ids_verified: 1,
    operations: 12,
    transactions: 5,
    days_missing: 4,
    counts_reason: null,
    ids_failing: 1,
    weekly_active_accounts: { average: 42, latest: 40, weeks_counted: 2, weeks_missing: 1 },
  },
  link_two_way: true,
  web_series: days.map((d) => ({
    day: d,
    aggregated: true,
    pageviews: 10,
    visitors: 3,
    visits: 4,
  })),
  api_series: days.map((d) => ({ day: d, requests: 0 })),
  onchain_series: days.map((d) => ({ day: d, ids_read: 1, operations: 3, transactions: 1 })),
  contract_functions: [
    {
      strkey: f.C_KEY,
      functions: {
        days_read: 2,
        days_no_call_tree: 1,
        days_missing: 0,
        functions: [
          {
            function: "swap",
            caller: "account",
            successful: 17,
            failed: null,
            peak_daily_accounts: 6,
            days_with_calls: 3,
          },
        ],
      },
    },
  ],
  cohort: {
    tenant_id: f.TENANT_ID,
    confirmed: null,
    confirmed_at: null,
    proposal: null,
    proposed_at: null,
  },
  percentile_cohort: { program: null, round: null },
  comparison: {
    cohort: { program: null, round: null, category: null, award_date: null },
    cohort_size: 3,
    kpis: {
      visitors: { value: 300, previous: 200, change: 0.5, percentile: 1 },
      pageviews: { value: 1234, previous: 1234, change: 0, percentile: 0.5 },
      requests: { value: 0, previous: 0, change: null, percentile: null },
      error_rate: { value: null, previous: null, change: null, percentile: null },
      onchain_operations: { value: 12, previous: 0, change: null, percentile: 0.5 },
    },
  },
  rules_met: [],
};

describe("scf review figures (contract 16.3)", () => {
  function answer(scf: unknown) {
    return routeFetch((_method, url) => {
      if (url.includes("/scf-view")) return { status: 200, body: scf };
      if (url.includes("/reports")) return { status: 200, body: { items: [] } };
      if (url.includes("/sites")) return { status: 200, body: { items: [], next_cursor: null } };
      return { status: 404, body: f.errorBody("not_found") };
    });
  }

  it("shows the operator rows, one definition per label and the contract calls", async () => {
    answer(scfBody);
    const host = await renderRoutes(
      routes(<ScfReviewPage />, "scf"),
      `/projects/${f.TENANT_ID}/scf`,
    );
    await waitFor(() => expect(host.querySelector("[data-check=web]")).not.toBeNull());
    // the control plane sent no grade, so none is drawn
    expect(host.querySelector("[data-testid=standing-grade]")?.getAttribute("data-grade")).toBe(
      "none",
    );
    const link = [...host.querySelectorAll("a")].find((a) => a.textContent?.includes("Export CSV"));
    expect(link?.getAttribute("href")).toBe(
      `/api/tenants/${f.TENANT_ID}/overview.csv?from=${days[0]}&to=${days[3]}`,
    );
    expect(host.querySelector("[data-check=web]")?.textContent).toContain("+50%");
    // the api has no request: its check shows no figure and its evidence says so
    await click(host.querySelector("[data-check=api]") as HTMLElement);
    await waitFor(() => expect(host.querySelector("[data-evidence=api]")).not.toBeNull());
    expect(host.querySelector("[data-figure=routes]")).toBeNull();
    // the on-chain evidence: "Days without data" reads as the on-chain page
    // counts it, and each verified contract's calls per function
    await click(host.querySelector("[data-check=onchain]") as HTMLElement);
    await waitFor(() => expect(host.querySelector("[data-evidence=onchain]")).not.toBeNull());
    expect(host.textContent).toContain("1 of 2");
    expect(host.textContent).toContain("Linked both ways");
    expect(host.querySelector("[data-figure=days-missing]")?.textContent).toContain(
      "before today without a ledger read",
    );
    // a value the view does not have is the missing glyph, never n/a
    expect(host.textContent).not.toContain("n/a");
    const table = host.querySelector(`[aria-label="Calls per function of ${f.C_KEY}"]`);
    const row = table?.querySelector('[data-function="swap"][data-caller="account"]');
    expect(row?.textContent).toContain("17");
    expect(row?.querySelector("[data-missing]")).not.toBeNull();
    expect(host.textContent).toContain("2 days read, 1 with calls of accounts only");
  });

  it("draws the grade in the seal when the field arrives", async () => {
    answer({
      ...scfBody,
      integrity: { grade: "B", graded_days: 8, days_a: 5, days_b: 3, days_c: 0 },
    });
    const host = await renderRoutes(
      routes(<ScfReviewPage />, "scf"),
      `/projects/${f.TENANT_ID}/scf`,
    );
    await waitFor(() => expect(host.querySelector("[data-testid=standing-grade]")).not.toBeNull());
    const seal = host.querySelector("[data-testid=standing-grade]");
    expect(seal?.getAttribute("data-grade")).toBe("B");
    expect(seal?.textContent).toContain("5 A days, 3 B days, 0 C days");
    expect(host.querySelector("[data-check=integrity]")?.textContent).toContain("Grade B");
  });

  it("leaves out on-chain counts that measured nothing, with no zero", async () => {
    answer({
      ...scfBody,
      onchain: {
        ...scfBody.onchain,
        operations: null,
        transactions: null,
        days_missing: null,
        counts_reason: "nothing_read",
      },
      onchain_series: scfBody.onchain_series.map((p) => ({
        ...p,
        ids_read: 0,
        operations: null,
        transactions: null,
      })),
    });
    const host = await renderRoutes(
      routes(<ScfReviewPage />, "scf"),
      `/projects/${f.TENANT_ID}/scf`,
    );
    await waitFor(() => expect(host.querySelector("[data-check=onchain]")).not.toBeNull());
    expect(host.querySelector("[data-check=onchain]")?.textContent).toContain("no ledger read yet");
    await click(host.querySelector("[data-check=onchain]") as HTMLElement);
    // a figure that holds no value is left out, never shown as 0
    await waitFor(() => expect(host.textContent).toContain("Verified IDs"));
    expect(host.querySelector("[data-figure=days-missing]")).toBeNull();
    expect(host.textContent).not.toContain("Transactions");
    expect(host.textContent).not.toContain("What SCF reviewers see did not load");
  });

  it("says when the project is not in the scf views", async () => {
    answer({ ...scfBody, in_operator_views: false });
    const host = await renderRoutes(
      routes(<ScfReviewPage />, "scf"),
      `/projects/${f.TENANT_ID}/scf`,
    );
    await waitFor(() => expect(host.textContent).toContain("SCF reviewers do not see it"));
    expect(host.querySelector("[data-check]")).toBeNull();
    expect(host.textContent).toContain("Signed reports");
  });
});

describe("operator on-chain column and exports (contract 15.7 and 16.1)", () => {
  it("keeps the on-chain summary of a list row", () => {
    const row = webProject.parse({
      tenant: {
        id: f.TENANT_ID,
        slug: "stellar-dex",
        name: "Stellar DEX",
        status: "active",
        created_at: "2026-11-12T09:00:00Z",
      },
      sites: 1,
      sites_verified: 1,
      sites_with_stellar_toml: 1,
      has_web_data: true,
      has_api_data: false,
      pageviews: 1,
      visitors: 1,
      visits: 1,
      bounces: 0,
      totaltime: 0,
      bounce_rate: 0,
      avg_visit_duration: 0,
      quarantine_pageviews: 0,
      quarantine_visitors: 0,
      quarantine_share: 0,
      ingest_requests: 1,
      ingest_rejected: 0,
      rejected_share: 0,
      top: { referrer: [], country: [], device: [], browser: [] },
      integrity: { grade: "A", graded_days: 3, days_a: 3, days_b: 0, days_c: 0 },
      coverage: { script_requests: 0, gpc_requests: 0, unmeasured_share: null },
      onchain: scfBody.onchain,
      ...scfBody.comparison,
    });
    expect(row.onchain?.operations).toBe(12);
  });

  it("draws the cell and exports exactly the rows on screen", async () => {
    const host = await render(
      <p>
        <OnchainValue s={scfBody.onchain} />
        <OnchainValue s={undefined} />
      </p>,
    );
    const cell = host.querySelector("[data-col=onchain]");
    expect(cell?.textContent).toContain("12");
    // the rest of the summary is read with the number, not only on hover
    expect(cell?.textContent).toContain("1 of 2 IDs verified");
    expect(cell?.textContent).toContain(
      "Weekly active accounts: 42 a week on average over 2 weeks, 1 missing",
    );
    expect(host.textContent).toContain("No accounts or contracts declared");
    expect(
      listExportHref(
        "web",
        {
          range: { from: "", to: "" },
          filters: { ...EMPTY_FILTERS, statuses: ["active"] },
          sort: "visitors",
          order: "desc",
        },
        { from: "2026-10-18", to: "2026-11-16" },
        75,
      ),
    ).toBe(
      "/api/operator/web/projects.csv?status=active&sort=visitors&order=desc&from=2026-10-18&to=2026-11-16&rows=75",
    );
    expect(
      exportPaths.compare("api", [f.TENANT_ID], { from: "2026-11-01", to: "2026-11-16" }),
    ).toBe(`/api/operator/api/compare.csv?from=2026-11-01&to=2026-11-16&tenants=${f.TENANT_ID}`);
  });
});

// a verified contract with calls on three days: one read whole, one with
// the calls of accounts only, one not read (contract 15.8)
const FN_CONTRACT_ID = "0193a6f8-9999-7caa-bdbb-eccddeeff002";
const functionsSummaryBody = {
  days_read: 1,
  days_no_call_tree: 1,
  days_missing: 1,
  functions: [
    {
      function: "swap",
      caller: "account",
      successful: 9,
      failed: null,
      peak_daily_accounts: 4,
      days_with_calls: 2,
    },
    {
      function: "{other}",
      caller: "contract",
      successful: 2,
      failed: 1,
      peak_daily_accounts: 1,
      days_with_calls: 1,
    },
  ],
};
const functionsBody = {
  ...functionsSummaryBody,
  id: FN_CONTRACT_ID,
  strkey: f.C_KEY,
  kind: "contract",
  verified: true,
  from: days[0],
  to: days[3],
  days: [
    { day: days[0], state: "read", missing: null },
    { day: days[1], state: "no_call_tree", missing: null },
    { day: days[2], state: null, missing: "day_over_read_cap" },
    { day: days[3], state: null, missing: "day_not_complete" },
  ],
  functions: [
    {
      ...functionsSummaryBody.functions[0],
      series: [
        { day: days[0], successful: 5, failed: null, accounts: 3 },
        { day: days[1], successful: 4, failed: null, accounts: 4 },
        { day: days[2], successful: null, failed: null, accounts: null },
        { day: days[3], successful: null, failed: null, accounts: null },
      ],
    },
    {
      ...functionsSummaryBody.functions[1],
      series: [
        { day: days[0], successful: 2, failed: 1, accounts: 1 },
        { day: days[1], successful: null, failed: null, accounts: null },
        { day: days[2], successful: null, failed: null, accounts: null },
        { day: days[3], successful: null, failed: null, accounts: null },
      ],
    },
  ],
};

describe("calls per contract function (contract 15.8)", () => {
  const verifiedContract = onchainView.parse({
    ...viewBody,
    ids: [
      {
        ...viewBody.ids[1],
        id: FN_CONTRACT_ID,
        verification: { ...none, status: "verified", method: "contract_admin" },
        functions: functionsSummaryBody,
      },
    ],
  }).ids[0];

  it("shows the totals per function and caller, failed missing for accounts", async () => {
    if (!verifiedContract) throw new Error("no id");
    const host = await render(<ContractFunctionsBlock id={verifiedContract} />);
    expect(host.querySelector("[data-testid=functions-days]")?.textContent).toBe(
      "1 day read, 1 with calls of accounts only, 1 without data",
    );
    const swap = host.querySelector('[data-function="swap"]');
    expect(swap?.textContent).toContain("Accounts");
    expect(swap?.querySelector("[data-missing]")).not.toBeNull();
    const other = host.querySelector('[data-function="{other}"]');
    expect(other?.textContent).toContain("Other functions");
    expect(other?.textContent).toContain("Contracts");
    // without a source there is nothing to open or export
    expect(host.textContent).not.toContain("Daily calls");
  });

  it("draws nothing for an unverified contract, whose calls do not count", async () => {
    const id = onchainView.parse(viewBody).ids[1];
    if (!id) throw new Error("no id");
    const host = await render(<ContractFunctionsBlock id={id} />);
    expect(host.textContent).toBe("");
  });

  it("loads the daily calls from the tenant route and links the export", async () => {
    if (!verifiedContract) throw new Error("no id");
    const calls = stubFetch([{ status: 200, body: functionsBody }]);
    const src = { tenantId: f.TENANT_ID, range: { from: days[0], to: days[3] } };
    const host = await render(<ContractFunctionsBlock id={verifiedContract} source={src} />);
    const link = [...host.querySelectorAll("a")].find((a) => a.textContent?.includes("Export CSV"));
    expect(link?.getAttribute("href")).toBe(
      `/api/tenants/${f.TENANT_ID}/onchain/ids/${FN_CONTRACT_ID}/functions.csv?from=${days[0]}&to=${days[3]}`,
    );
    await click(byRole(host, "button", "Daily calls"));
    await waitFor(() => expect(host.textContent).toContain("successful calls per day"));
    expect(calls[0]?.url).toBe(
      `/api/tenants/${f.TENANT_ID}/onchain/ids/${FN_CONTRACT_ID}/functions?from=${days[0]}&to=${days[3]}`,
    );
  });

  it("draws a day not read as missing with its reason, never as zero", async () => {
    const data = contractFunctions.parse(functionsBody);
    const host = await render(<FunctionsDaily data={data} />);
    expect(host.textContent).toContain("1 day without data. More activity than one run reads");
    expect(host.textContent).toContain("1 day with the calls of accounts only");
    const strip = host.querySelector('[aria-label="Daily calls of Other functions by contracts"]');
    expect(strip?.querySelectorAll(".bg-series-1")).toHaveLength(1);
    expect(strip?.querySelectorAll(".border-dashed")).toHaveLength(3);
    const titles = [...(strip?.querySelectorAll("[title]") ?? [])].map((e) =>
      e.getAttribute("title"),
    );
    expect(titles[1]).toContain("only the calls of accounts are counted");
  });

  it("reads the operator route for an operator source", () => {
    expect(
      exportPaths.functions({ tenantId: f.TENANT_ID, operator: true, range: {} }, FN_CONTRACT_ID),
    ).toBe(`/api/operator/tenants/${f.TENANT_ID}/onchain/ids/${FN_CONTRACT_ID}/functions.csv`);
  });
});

describe("one missing-value rule for on-chain figures", () => {
  it("shows 0 only after a read of a verified id", () => {
    expect(chainGap(0, [{ operations: 4 }])).toBe(NO_VERIFIED_IDS);
    expect(chainGap(1, [{ operations: null }, { operations: null }])).toBe(NO_LEDGER_READ);
    expect(chainGap(1, [{ operations: null }, { operations: 0 }])).toBeNull();
  });
});
