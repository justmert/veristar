import { Outlet } from "react-router";
import { describe, expect, it } from "vitest";
import { exportPaths, onchainView } from "@/api/onchain";
import { webProject } from "@/api/schemas";
import { OnchainPanel } from "@/components/onchain";
import { EMPTY_FILTERS } from "@/lib/global";
import { OnchainPage } from "@/pages/onchain";
import { ExportListButton, OnchainCell } from "@/pages/operator/onchain-parts";
import type { ProjectContext } from "@/pages/project-layout";
import { IntegritySlot, SCFViewPage } from "@/pages/scf-view";
import * as f from "./fixtures/contract";
import { byLabel, byRole, click, render, renderRoutes, stubFetch, type, waitFor } from "./helpers";

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
    },
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

describe("on-chain panel (contract 15.3)", () => {
  it("parses the view and shows totals, proofs and why days are missing", async () => {
    const view = onchainView.parse(viewBody);
    const host = await render(<OnchainPanel view={view} />);
    expect(host.textContent).toContain("On-chain");
    expect(host.textContent).toContain("1 of 2");
    expect(host.textContent).toContain("Verified");
    expect(host.textContent).toContain("Unverified");
    expect(host.textContent).toContain("stellar.toml linked both ways");
    // a failed read is a missing day with its reason, not a zero
    expect(host.textContent).toContain("3 days without data: The ledger API refused more requests");
    expect(host.textContent).toContain(
      "1 day without data: Before the first day read for this account",
    );
    // a stored zero is drawn as a bar, a missing day as a dashed cell
    const strip = host.querySelector(`[aria-label="Daily operations of ${f.G_KEY}"]`);
    expect(strip?.querySelectorAll(".bg-series-1")).toHaveLength(2);
    expect(strip?.querySelectorAll(".border-dashed")).toHaveLength(2);
  });

  it("says what to do when nothing is declared", async () => {
    const view = onchainView.parse({
      ...viewBody,
      ids: [],
      totals: { ...viewBody.totals, ids: 0, ids_verified: 0, accounts: 0, contracts: 0 },
      link: { two_way: false, sites: [] },
    });
    const host = await render(<OnchainPanel view={view} />);
    expect(host.textContent).toContain("No accounts or contracts declared yet");
  });
});

describe("accounts and contracts page (contract 15.4)", () => {
  const path = `/projects/${f.TENANT_ID}/onchain`;

  it("adds an id and refuses a malformed one before sending", async () => {
    const calls = stubFetch([
      { status: 200, body: viewBody },
      {
        status: 201,
        body: {
          id: "0193a6f8-8888-7caa-bdbb-eccddeeff001",
          strkey: f.G_KEY,
          kind: "account",
          added_by: "console",
          created_at: "2026-11-16T09:00:00Z",
          verification: none,
        },
      },
      { status: 200, body: viewBody },
    ]);
    const host = await renderRoutes(routes(<OnchainPage />, "onchain"), path);
    await waitFor(() => expect(host.textContent).toContain("Accounts and contracts"));
    await type(byLabel(host, "Account or contract"), "not-a-key");
    await click(byRole(host, "button", "Add"));
    expect(host.textContent).toContain("Enter a Stellar account (G…) or a Soroban contract (C…)");
    expect(calls).toHaveLength(1);
    await type(byLabel(host, "Account or contract"), f.G_KEY);
    await click(byRole(host, "button", "Add"));
    await waitFor(() => expect(calls).toHaveLength(3));
    expect(calls[1]?.method).toBe("POST");
    expect(calls[1]?.url).toBe(`/api/tenants/${f.TENANT_ID}/onchain/ids`);
    expect(calls[1]?.headers.get("X-Requested-With")).toBe("sa-console");
    expect(calls[1]?.body).toEqual({ strkey: f.G_KEY });
  });

  it("proves an account with a data entry", async () => {
    const calls = stubFetch([
      { status: 200, body: viewBody },
      {
        status: 200,
        body: {
          id: ACCOUNT_ID,
          account: f.G_KEY,
          message: `Veristar ownership proof\nProject: ${f.TENANT_ID}\nAccount: ${f.G_KEY}\nNonce: 00112233445566778899aabbccddeeff\nExpires: 2026-11-16T10:00:00Z`,
          manage_data: { name: "veristar", value: "00112233445566778899aabbccddeeff" },
          expires_at: "2026-11-16T10:00:00Z",
        },
      },
      {
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
      { status: 200, body: viewBody },
    ]);
    const host = await renderRoutes(routes(<OnchainPage />, "onchain"), path);
    await waitFor(() => expect(host.textContent).toContain("Prove again"));
    await click(byRole(host, "button", "Prove again"));
    await waitFor(() => expect(host.textContent).toContain("Or publish a data entry"));
    expect(host.textContent).toContain("00112233445566778899aabbccddeeff");
    expect(host.textContent).toContain("Veristar ownership proof");
    await click(byRole(host, "button", "Check the data entry"));
    await waitFor(() => expect(calls).toHaveLength(4));
    expect(calls[1]?.url).toBe(`/api/tenants/${f.TENANT_ID}/onchain/ids/${ACCOUNT_ID}/challenge`);
    expect(calls[2]?.url).toBe(`/api/tenants/${f.TENANT_ID}/onchain/ids/${ACCOUNT_ID}/verify`);
    expect(calls[2]?.body).toEqual({ method: "manage_data" });
  });

  it("checks a contract's admin and shows the last error in words", async () => {
    const calls = stubFetch([
      { status: 200, body: viewBody },
      {
        status: 200,
        body: {
          id: CONTRACT_ID,
          strkey: f.C_KEY,
          kind: "contract",
          added_by: "console",
          created_at: "2026-11-13T09:00:00Z",
          verification: none,
        },
      },
      { status: 200, body: viewBody },
    ]);
    const host = await renderRoutes(routes(<OnchainPage />, "onchain"), path);
    await waitFor(() => expect(host.textContent).toContain("Check the admin"));
    expect(host.textContent).toContain(
      "Last check: The contract stores no Admin or Owner entry that can be read.",
    );
    // the two-way link legs of the verified domain
    expect(host.textContent).toContain("Its [DOCUMENTATION] ORG_URL is on example.org");
    await click(byRole(host, "button", "Check the admin"));
    await waitFor(() => expect(calls).toHaveLength(3));
    expect(calls[1]?.body).toEqual({ method: "contract_admin" });
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
    ids_failing: 1,
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
};

describe("what scf sees (contract 16.3)", () => {
  it("shows the operator rows and no grade the control plane did not send", async () => {
    stubFetch([{ status: 200, body: scfBody }]);
    const host = await renderRoutes(
      routes(<SCFViewPage />, "scf-view"),
      `/projects/${f.TENANT_ID}/scf-view`,
    );
    await waitFor(() => expect(host.textContent).toContain("1,234"));
    expect(host.textContent).toContain("1 of 2");
    expect(host.textContent).toContain("stellar.toml linked both ways");
    expect(host.querySelector("[data-testid=integrity-grade]")).toBeNull();
    const link = [...host.querySelectorAll("a")].find((a) => a.textContent?.includes("Export CSV"));
    expect(link?.getAttribute("href")).toBe(
      `/api/tenants/${f.TENANT_ID}/overview.csv?from=${days[0]}&to=${days[3]}`,
    );
  });

  it("renders the integrity grade when the field arrives", async () => {
    const host = await render(
      <IntegritySlot value={{ grade: "B", graded_days: 8, days_a: 5, days_b: 3, days_c: 0 }} />,
    );
    expect(host.querySelector("[data-testid=integrity-grade]")?.textContent).toBe("B");
    expect(host.textContent).toContain("8 graded site days: 5 A, 3 B, 0 C");
    const empty = await render(<IntegritySlot value={undefined} />);
    expect(empty.textContent).toBe("");
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
      onchain: scfBody.onchain,
    });
    expect(row.onchain?.operations).toBe(12);
  });

  it("draws the cell and exports exactly the rows on screen", async () => {
    const host = await render(
      <table>
        <tbody>
          <tr>
            <OnchainCell s={scfBody.onchain} />
            <OnchainCell s={undefined} />
          </tr>
        </tbody>
      </table>,
    );
    const cells = host.querySelectorAll("[data-col=onchain]");
    expect(cells[0]?.textContent).toContain("12");
    expect(cells[0]?.textContent).toContain("1/2 verified");
    const exp = await render(
      <ExportListButton
        view="web"
        state={{
          range: { from: "", to: "" },
          filters: { ...EMPTY_FILTERS, statuses: ["active"] },
          sort: "visitors",
          order: "desc",
        }}
        resolved={{ from: "2026-10-18", to: "2026-11-16" }}
        rows={75}
      />,
    );
    expect(exp.querySelector("a")?.getAttribute("href")).toBe(
      "/api/operator/web/projects.csv?status=active&sort=visitors&order=desc&from=2026-10-18&to=2026-11-16&rows=75",
    );
    expect(
      exportPaths.compare("api", [f.TENANT_ID], { from: "2026-11-01", to: "2026-11-16" }),
    ).toBe(`/api/operator/api/compare.csv?from=2026-11-01&to=2026-11-16&tenants=${f.TENANT_ID}`);
  });
});
