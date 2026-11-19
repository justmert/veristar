// cohorts, threshold rules and signed reports (contract 19 and 20) in the
// console: the global filters and change columns, the award annotation,
// the rules page, the reports page and the public report page that checks
// a signature in the browser
import { createHash, generateKeyPairSync, sign, webcrypto } from "node:crypto";
import { act } from "react";
import { Outlet } from "react-router";
import { describe, expect, it, vi } from "vitest";
import { reportDocument } from "@/api/reports";
import { OperatorCohortPanel } from "@/components/cohort";
import { canonicalize, crc16xmodem } from "@/lib/report-verify";
import { withCohort } from "@/pages/home/enroll";
import { ComparePage } from "@/pages/operator/compare";
import { OperatorProjectsPage } from "@/pages/operator/projects";
import { RulesPage } from "@/pages/operator/rules";
import { ScfReviewPage, SignedReports } from "@/pages/project/scf";
import type { ProjectContext } from "@/pages/project-layout";
import { PublicReportPage } from "@/pages/public/report";
import * as c from "./fixtures/contract";
import * as f from "./fixtures/global";
import {
  byLabel,
  byRole,
  click,
  renderRoutes,
  routeFetch,
  stubFetch,
  type,
  waitFor,
} from "./helpers";

const EM_DASH = String.fromCharCode(0x2014);

async function select(el: HTMLSelectElement, value: string) {
  await act(async () => {
    el.value = value;
    el.dispatchEvent(new Event("change", { bubbles: true }));
  });
}

describe("global views with cohorts (contract 19.1 to 19.3)", () => {
  it("sends the cohort filters and shows change, percentile and cohort per row", async () => {
    window.localStorage.setItem(
      "veristar.operator.projects.columns.web",
      JSON.stringify([
        "cohort",
        "visitors_growth",
        "pageviews_growth",
        "onchain_operations_growth",
        "visitors_percentile",
      ]),
    );
    const big = {
      ...f.webProjectsBody,
      items: [{ ...f.webProjectA, cohort_size: 12 }, f.webProjectB],
    };
    const calls = routeFetch((_m, url) =>
      url === "/api/operator/cohorts"
        ? { status: 200, body: f.cohortListBody }
        : { status: 200, body: url.includes("program=") ? f.webProjectsBody : big },
    );
    const host = await renderRoutes(
      [{ path: "/operator/projects", element: <OperatorProjectsPage /> }],
      "/operator/projects?view=web",
    );
    await waitFor(() => expect(host.textContent).toContain("Stellar DEX"));
    const row = () => host.querySelector<HTMLElement>(`tr[data-tenant="${c.TENANT_ID}"]`);
    expect(row()?.querySelector("[data-cohort]")?.textContent).toBe(
      "Build Award, SCF #38, DeFi, awarded Nov 11, 2026",
    );
    const changes = [...(row()?.querySelectorAll("[data-kpi=change]") ?? [])].map(
      (e) => e.textContent,
    );
    expect(changes).toEqual(["+100%", "-20%", "–No value in the previous range"]);
    expect(row()?.querySelector("[data-kpi=percentile]")?.textContent).toBe("P75");

    await select(byLabel(host, "SCF program") as unknown as HTMLSelectElement, "Build Award");
    await waitFor(() =>
      expect(calls.at(-1)?.url).toBe(
        "/api/operator/web/projects?program=Build%20Award&sort=pageviews&order=desc&limit=50",
      ),
    );
    // a cohort of four is too small for a percentile
    await waitFor(() =>
      expect(row()?.querySelector("[data-kpi=percentile]")?.textContent).toContain(
        "Fewer than 5 projects in the cohort",
      ),
    );
    await select(byLabel(host, "SCF round") as unknown as HTMLSelectElement, "SCF #38");
    await waitFor(() => expect(calls.at(-1)?.url).toContain("round=SCF%20%2338"));

    const header = [...host.querySelectorAll("th button")].find(
      (b) => b.textContent === "Visitors change",
    );
    await click(header as HTMLElement);
    await waitFor(() => expect(calls.at(-1)?.url).toContain("sort=visitors_growth&order=desc"));
    const pctHead = [...host.querySelectorAll("th button")].find(
      (b) => b.textContent === "Visitors percentile",
    );
    expect(pctHead?.getAttribute("aria-description")).toContain(
      "The cohort filters set the cohort",
    );
    window.localStorage.clear();
  });

  it("marks each compared project's award date on the charts", async () => {
    stubFetch([
      { status: 200, body: f.webCompareBody },
      { status: 200, body: f.apiCompareBody },
    ]);
    const host = await renderRoutes(
      [{ path: "/operator/compare", element: <ComparePage /> }],
      `/operator/compare?tenants=${c.TENANT_ID},${f.TENANT_B}`,
    );
    await waitFor(() => expect(host.textContent).toContain("payments-api"));
    const markers = [...host.querySelectorAll("[data-marker='2026-11-11']")];
    expect(markers.length).toBeGreaterThan(4);
    expect(markers[0]?.textContent).toContain("Stellar DEX awarded");
    expect(
      host.querySelector("section[aria-label='Compared projects'] [data-cohort]")?.textContent,
    ).toBe("Build Award, SCF #38, DeFi");
  });
});

const rule = {
  id: "0193a6fc-1111-7f77-8a88-b99caadbbecc",
  name: "Steady web",
  metric: "visitors",
  min_per_day: 100,
  min_days: 3,
  months: 2,
  min_grade: "B",
  cohort: { program: "Build Award", round: null, category: null },
  created_at: "2026-11-16T09:00:00Z",
  updated_at: "2026-11-16T09:00:00Z",
};

const matches = {
  rule,
  as_of: "2026-11-16",
  from: "2026-09-01",
  to: "2026-10-31",
  months: ["2026-09", "2026-10"],
  evaluated: 3,
  items: [
    {
      tenant: f.refA,
      cohort: f.cohortA,
      meets: true,
      integrity: { grade: "A", graded_days: 2, days_a: 2, days_b: 0, days_c: 0 },
      months: [
        { month: "2026-09", qualifying_days: 3, days: [{ day: "2026-09-01", value: 120 }] },
        { month: "2026-10", qualifying_days: 4, days: [{ day: "2026-10-01", value: 100 }] },
      ],
    },
  ],
};

describe("threshold rules (contract 19.4)", () => {
  it("creates a rule, lists its matches with evidence and deletes it", async () => {
    let saved = false;
    let deleted = false;
    const calls = routeFetch((method, url) => {
      if (method === "POST" && url === "/api/operator/rules") {
        saved = true;
        return { status: 201, body: rule };
      }
      if (method === "DELETE") {
        deleted = true;
        return { status: 204 };
      }
      if (url === "/api/operator/rules")
        return {
          status: 200,
          body: {
            items:
              saved && !deleted
                ? [{ ...rule, match_count: 1, evaluated: 3, matches_as_of: "2026-11-16" }]
                : [],
          },
        };
      if (url.startsWith(`/api/operator/rules/${rule.id}/matches`))
        return { status: 200, body: matches };
      if (url === "/api/operator/cohorts") return { status: 200, body: f.cohortListBody };
      return { status: 500 };
    });
    const host = await renderRoutes(
      [{ path: "/operator/rules", element: <RulesPage /> }],
      "/operator/rules",
    );
    await waitFor(() => expect(host.textContent).toContain("No rule is saved yet"));

    await click(byRole(host, "button", "New rule"));
    await click(byRole(host, "button", "Save rule"));
    expect(host.textContent).toContain("A whole number of at least 1.");
    expect(calls.some((x) => x.method === "POST")).toBe(false);

    await type(byLabel(host, "Name") as HTMLInputElement, " Steady web ");
    await type(byLabel(host, "Minimum per day") as HTMLInputElement, "100");
    await type(byLabel(host, "Days per month") as HTMLInputElement, "3");
    await type(byLabel(host, "Complete months") as HTMLInputElement, "2");
    await select(byLabel(host, "Integrity grade") as unknown as HTMLSelectElement, "B");
    // the cohort is chosen from the confirmed values
    await waitFor(() =>
      expect((byLabel(host, "SCF program") as unknown as HTMLSelectElement).disabled).toBe(false),
    );
    await select(byLabel(host, "SCF program") as unknown as HTMLSelectElement, "Build Award");
    await click(byRole(host, "button", "Save rule"));
    await waitFor(() => expect(calls.find((x) => x.method === "POST")).toBeDefined());
    expect(calls.find((x) => x.method === "POST")?.body).toEqual({
      name: "Steady web",
      metric: "visitors",
      min_per_day: 100,
      min_days: 3,
      months: 2,
      min_grade: "B",
      cohort: { program: "Build Award", round: null, category: null },
    });

    // saving opens the rule's matches
    await waitFor(() => expect(host.textContent).toContain("1 of 3 projects meet the rule"));
    expect(host.textContent).toContain("2026-09: 3 qualifying days");
    expect(host.textContent).toContain("Sep 1, 2026: 120");
    expect(host.textContent).toContain("Grade A");
    // the list says how many projects meet each rule, from the list itself
    await waitFor(() =>
      expect(host.querySelector(`[data-matches="${rule.id}"]`)?.textContent).toBe("1 of 3"),
    );
    const csv = [...host.querySelectorAll("a")].find((a) => a.textContent?.includes("Export CSV"));
    expect(csv?.getAttribute("href")).toBe(`/api/operator/rules/${rule.id}/matches.csv`);
    expect(host.textContent).toContain(
      "Verified visitors of at least 100 a day on 3 or more days in each of the last 2 complete months, integrity grade B or better",
    );

    await act(async () => {
      (
        host.querySelector(`button[aria-label="Actions for ${rule.name}"]`) as HTMLElement
      ).dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, button: 0 }));
    });
    await click(
      [...host.ownerDocument.querySelectorAll<HTMLElement>("[role=menuitem]")].find(
        (m) => m.textContent === "Delete rule",
      ) as HTMLElement,
    );
    const dialog = [...host.querySelectorAll("dialog")].find((d) => d.hasAttribute("open"));
    const confirm = [...(dialog?.querySelectorAll("button") ?? [])].find(
      (b) => b.textContent === "Delete rule",
    );
    await click(confirm as HTMLElement);
    await waitFor(() =>
      expect(calls.find((x) => x.method === "DELETE")?.url).toBe(`/api/operator/rules/${rule.id}`),
    );
    expect(host.textContent).not.toContain(EM_DASH);
  });
});

// ---- signed reports

// a stellar account id of a raw ed25519 public key (sep-23), written here so
// the test signs with node's own ed25519 and no wallet code
function accountId(raw: Uint8Array): string {
  const body = new Uint8Array(33);
  body[0] = 6 << 3;
  body.set(raw, 1);
  const crc = crc16xmodem(body);
  const all = new Uint8Array([...body, crc & 0xff, crc >>> 8]);
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
  let bits = 0;
  let value = 0;
  let out = "";
  for (const b of all) {
    value = (value << 8) | b;
    bits += 8;
    while (bits >= 5) {
      out += alphabet[(value >>> (bits - 5)) & 31];
      bits -= 5;
    }
  }
  if (bits > 0) out += alphabet[(value << (5 - bits)) & 31];
  return out;
}

const keys = generateKeyPairSync("ed25519");
const rawPub = new Uint8Array(keys.publicKey.export({ format: "der", type: "spki" }).subarray(12));
const SIGNER = accountId(rawPub);

// sep-53 with node's own primitives: sha-256 of the prefix and the message,
// signed with ed25519
function sep53(message: string): string {
  const hash = createHash("sha256").update(`Stellar Signed Message:\n${message}`, "utf8").digest();
  return sign(null, hash, keys.privateKey).toString("base64");
}

const document = {
  format: "veristar-usage-report",
  version: 1,
  id: "abcdefghijklmnopqrstuvwxyz",
  issuer: {
    name: "Veristar",
    console: "https://app.veristar.example",
    stellar_toml: "https://app.veristar.example/.well-known/stellar.toml",
    signing_key: SIGNER,
  },
  project: { id: c.TENANT_ID, slug: "stellar-dex", name: "Stellar DEX" },
  period: { from: "2026-10-01", to: "2026-10-31", days: 31 },
  generated_at: "2026-11-16T09:00:00Z",
  methods: {
    format: 1,
    web_metrics: 1,
    api_metrics: 1,
    onchain: 1,
    cohort: 1,
    integrity_rules: 1,
    route_normaliser: 2,
    platform: "v0.3.0",
  },
  web: {
    pageviews: 9000,
    visitors: 4321,
    visits: 5000,
    bounces: 1000,
    totaltime_seconds: 100000,
    bounce_rate: "0.200000",
    avg_visit_duration_seconds: "20.000000",
    quarantine_pageviews: 10,
    quarantine_visitors: 2,
    quarantine_share: "0.001110",
    gateway_requests: 9100,
    gateway_rejected: 90,
    sites: 1,
    sites_verified: 1,
    series: [{ day: "2026-10-01", aggregated: true, pageviews: 300, visitors: 140, visits: 160 }],
  },
  api: {
    requests: 0,
    requests_4xx: 0,
    requests_5xx: 0,
    rate_4xx: null,
    rate_5xx: null,
    p50: { seconds: null, over_10s: false },
    p95: { seconds: null, over_10s: false },
    p99: { seconds: null, over_10s: false },
    services: 0,
    routes: 0,
    source: null,
    series: [],
  },
  onchain: {
    ids: 1,
    ids_verified: 1,
    operations: 77,
    transactions: 70,
    days_missing: 0,
    two_way_link: true,
    series: [],
  },
  integrity: {
    grade: "A",
    graded_days: 31,
    days_a: 31,
    days_b: 0,
    days_c: 0,
    reasons: [],
  },
  cohort: {
    program: "Build Award",
    round: "SCF #38",
    category: "DeFi",
    award_date: "2026-09-01",
    percentile_program: "Build Award",
    percentile_round: "SCF #38",
    cohort_size: 12,
    kpis: {
      visitors: {
        value: "4321.000000",
        previous: "2000.000000",
        change: "1.160500",
        percentile: "0.750000",
      },
    },
  },
  rules_met: [],
};

// format version 2: the rule versions that graded the period, coverage,
// has_data, lossy, infrastructure requests and weekly active accounts
const documentV2 = {
  ...document,
  version: 2,
  // a cohort below five projects: the page keeps its percentiles back
  cohort: { ...document.cohort, cohort_size: 3 },
  methods: { ...document.methods, format: 2, integrity_rules: [1, 2] },
  web: {
    ...document.web,
    has_data: true,
    coverage: { script_requests: 6000, gpc_requests: 600, unmeasured_share: "0.100000" },
  },
  api: { ...document.api, has_data: false, lossy: false, infrastructure_requests: 12 },
  onchain: {
    ...document.onchain,
    weekly_active_accounts: {
      average: "14.500000",
      latest: 15,
      weeks_counted: 2,
      weeks_missing: 2,
    },
  },
};

// format version 3: the calls per function of each verified contract
const FN_CONTRACT = "CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAG";
const documentV3 = {
  ...documentV2,
  version: 3,
  methods: { ...documentV2.methods, format: 3, onchain: 2 },
  onchain: {
    ...documentV2.onchain,
    counts_reason: null,
    contracts: [
      {
        strkey: FN_CONTRACT,
        days_read: 5,
        days_no_call_tree: 0,
        days_missing: 2,
        functions: [
          {
            function: "deposit",
            caller: "account",
            successful: 812,
            failed: null,
            peak_daily_accounts: 77,
            days_with_calls: 5,
          },
        ],
      },
    ],
  },
};

function publicBody(report: Record<string, unknown>, signer = SIGNER, signed: unknown = document) {
  return {
    id: "abcdefghijklmnopqrstuvwxyz",
    url: "https://app.veristar.example/r/abcdefghijklmnopqrstuvwxyz",
    created_at: "2026-11-16T09:00:00Z",
    file: {
      report,
      signature: { scheme: "sep-53", signer, value: sep53(canonicalize(signed)) },
    },
    keys: {
      current: SIGNER,
      retired: [],
      stellar_toml: "https://app.veristar.example/.well-known/stellar.toml",
    },
  };
}

const publicRoute = [{ path: "/r/:reportId", element: <PublicReportPage /> }];

describe("the public report page (contract 20.5)", () => {
  // the test dom has no webcrypto of its own; node's is the one browsers mirror
  const withCrypto = () => vi.stubGlobal("crypto", webcrypto);

  it("checks the signature in the browser and offers the json", async () => {
    withCrypto();
    const calls = stubFetch([{ status: 200, body: publicBody(document) }]);
    const host = await renderRoutes(publicRoute, "/r/abcdefghijklmnopqrstuvwxyz");
    await waitFor(() => expect(host.querySelector("[data-verdict=verified]")).not.toBeNull());
    expect(calls[0]?.url).toBe("/api/public/reports/abcdefghijklmnopqrstuvwxyz");
    // the plain line first, the standard and the key on demand
    expect(host.querySelector("[data-verdict=verified]")?.textContent).toBe(
      "Signed by Veristar. This browser checked the signature.",
    );
    expect(host.textContent).toContain("Veristar's current report key");
    expect(host.textContent).toContain(SIGNER);
    // the build metadata is the footer, after every figure
    expect(host.querySelector("footer")?.textContent).toContain("Route rules2");
    expect(host.textContent).toContain("4,321");
    expect(host.textContent).toContain("Grade A");
    // the figures lead, each with its change
    expect(host.querySelector("[data-hero=visitors]")?.textContent).toContain("+116%");
    expect(host.textContent).toContain("P75");
    expect(byRole(host, "button", "Download JSON")).toBeTruthy();
    expect(host.textContent).not.toContain(EM_DASH);
  });

  it("reads a version 2 report and still a stored version 1 report", async () => {
    withCrypto();
    expect(reportDocument.safeParse(document).success).toBe(true);
    expect(reportDocument.safeParse(documentV2).success).toBe(true);
    // a version 2 report without its new members is not a version 2 report
    const { coverage: _, ...webWithout } = documentV2.web;
    expect(reportDocument.safeParse({ ...documentV2, web: webWithout }).success).toBe(false);
    stubFetch([{ status: 200, body: publicBody(documentV2, SIGNER, documentV2) }]);
    const host = await renderRoutes(publicRoute, "/r/abcdefghijklmnopqrstuvwxyz");
    await waitFor(() => expect(host.querySelector("[data-verdict=verified]")).not.toBeNull());
    expect(host.textContent).toContain("4,321");
    expect(host.textContent).toContain("1, 2");
    // the members version 2 adds are shown
    expect(host.textContent).toContain("About 10% of visits are not measured");
    // a part with no data is left out, not listed as zeros and dashes
    expect(host.querySelector('section[aria-label="API"]')).toBeNull();
    expect(host.querySelector("[data-hero=requests]")).toBeNull();
    expect(host.textContent).toContain("15 a week on average over 2 weeks, 2 missing");
    // a cohort of three keeps its percentiles back: no column of dashes
    expect(host.textContent).not.toContain("P75");
    expect([...host.querySelectorAll("th")].some((th) => th.textContent === "Percentile")).toBe(
      false,
    );
    expect(host.querySelector("[data-missing]")).toBeNull();
  });

  it("reads a version 3 report with the calls per contract function", async () => {
    withCrypto();
    expect(reportDocument.safeParse(documentV3).success).toBe(true);
    // a version 3 report without its contracts is not a version 3 report
    const { contracts: _, ...onchainWithout } = documentV3.onchain;
    expect(reportDocument.safeParse({ ...documentV3, onchain: onchainWithout }).success).toBe(
      false,
    );
    stubFetch([{ status: 200, body: publicBody(documentV3, SIGNER, documentV3) }]);
    const host = await renderRoutes(publicRoute, "/r/abcdefghijklmnopqrstuvwxyz");
    await waitFor(() => expect(host.querySelector("[data-verdict=verified]")).not.toBeNull());
    const row = host.querySelector(`[data-contract="${FN_CONTRACT}"] [data-function="deposit"]`);
    expect(row?.textContent).toContain("812");
    expect(row?.querySelector("[data-missing]")).not.toBeNull();
    expect(host.textContent).toContain("5 days read, 2 without data");
    expect(host.textContent).toContain("15 a week on average over 2 weeks, 2 missing");
  });

  it("leaves out version 3 on-chain counts that measured nothing, never as zero", async () => {
    withCrypto();
    const unread = {
      ...documentV3,
      onchain: {
        ...documentV3.onchain,
        operations: null,
        transactions: null,
        days_missing: null,
        counts_reason: "nothing_read",
        series: [{ day: "2026-10-01", ids_read: 0, operations: null, transactions: null }],
        contracts: [],
      },
    };
    expect(reportDocument.safeParse(unread).success).toBe(true);
    // the reason is part of version 3: a null count without it is refused
    const { counts_reason: _, ...withoutReason } = unread.onchain;
    expect(reportDocument.safeParse({ ...unread, onchain: withoutReason }).success).toBe(false);
    // version 2 keeps numbers
    expect(
      reportDocument.safeParse({
        ...documentV2,
        onchain: { ...documentV2.onchain, operations: null },
      }).success,
    ).toBe(false);
    stubFetch([{ status: 200, body: publicBody(unread, SIGNER, unread) }]);
    const host = await renderRoutes(publicRoute, "/r/abcdefghijklmnopqrstuvwxyz");
    await waitFor(() => expect(host.querySelector("[data-verdict=verified]")).not.toBeNull());
    expect(host.querySelector("[data-hero=onchain_operations]")).toBeNull();
    expect(host.textContent).not.toContain("Transactions");
    expect(host.textContent).not.toContain("Days without data");
    // the signer key and the signature are random per run and may hold the
    // digits by chance, so they are dropped before looking for the count
    const figures = (host.textContent ?? "").replace(/[A-Za-z0-9+/=]{40,}/g, " ");
    expect(figures).toContain("Signature details");
    expect(figures).not.toContain("77");
  });

  it("titles a signature by a key Veristar does not publish as not Veristar's", async () => {
    withCrypto();
    const body = publicBody(document);
    const other = `G${"A".repeat(55)}`;
    stubFetch([{ status: 200, body: { ...body, keys: { ...body.keys, current: other } } }]);
    const host = await renderRoutes(publicRoute, "/r/abcdefghijklmnopqrstuvwxyz");
    await waitFor(() => expect(host.querySelector("[data-verdict=unknown-key]")).not.toBeNull());
    expect(host.textContent).toContain("Not signed by Veristar");
    expect(host.textContent).not.toContain("Signed by Veristar.");
    expect(host.querySelector("[data-verdict=unknown-key]")?.getAttribute("role")).toBe("alert");
  });

  it("says a changed report is not valid", async () => {
    withCrypto();
    const tampered = { ...document, web: { ...document.web, visitors: 9999 } };
    stubFetch([{ status: 200, body: publicBody(tampered) }]);
    const host = await renderRoutes(publicRoute, "/r/abcdefghijklmnopqrstuvwxyz");
    await waitFor(() => expect(host.querySelector("[data-verdict=invalid]")).not.toBeNull());
    expect(host.textContent).toContain("the signature does not match the report");
    // none of the changed figures is shown next to the verdict
    expect(host.textContent).not.toContain("9,999");
    expect(host.textContent).not.toContain("Stellar DEX");
  });

  it("shows an unknown link as not available", async () => {
    stubFetch([{ status: 404, body: c.errorBody("not_found") }]);
    const host = await renderRoutes(publicRoute, "/r/zzzzzzzzzzzzzzzzzzzzzzzzzz");
    await waitFor(() => expect(host.textContent).toContain("This report link is not available"));
    expect(host.textContent).not.toContain("revoked");
  });

  it("shows a revoked link (410 report_revoked) as revoked, apart from an unknown one", async () => {
    stubFetch([{ status: 410, body: c.errorBody("report_revoked") }]);
    const host = await renderRoutes(publicRoute, "/r/zzzzzzzzzzzzzzzzzzzzzzzzzz");
    await waitFor(() => expect(host.textContent).toContain("This report link was revoked"));
    expect(host.textContent).not.toContain("This report link is not available");
    expect(host.textContent).not.toContain("did not load");
  });
});

const ctx = {
  tenant: c.tenantBody,
  me: c.meBody,
  isOwner: true,
  termsCurrent: true,
} as unknown as ProjectContext;

function projectRoutes(page: React.ReactNode, path: string) {
  return [
    {
      path: "/projects/:tenantId",
      element: <Outlet context={ctx} />,
      children: [{ path, element: page }],
    },
  ];
}

const summary = {
  id: "abcdefghijklmnopqrstuvwxyz",
  url: "https://app.veristar.example/r/abcdefghijklmnopqrstuvwxyz",
  from: "2026-10-01",
  to: "2026-10-31",
  signer: SIGNER,
  created_at: "2026-11-16T09:00:00Z",
  created_by: c.USER_ID,
  revoked_at: null,
  view_count: 3,
  last_viewed_at: "2026-11-16T10:00:00Z",
};

describe("the reports page (contract 20.3 and 20.4)", () => {
  it("generates a report of closed days, lists it and revokes its link", async () => {
    let revoked = false;
    const calls = routeFetch((method, url) => {
      if (method === "POST" && url.endsWith("/reports"))
        return { status: 201, body: { ...summary, file: publicBody(document).file } };
      if (method === "POST" && url.endsWith("/revoke")) {
        revoked = true;
        return { status: 200, body: { ...summary, revoked_at: "2026-11-16T11:00:00Z" } };
      }
      return {
        status: 200,
        body: { items: [revoked ? { ...summary, revoked_at: "2026-11-16T11:00:00Z" } : summary] },
      };
    });
    const host = await renderRoutes(
      projectRoutes(<SignedReports />, "scf"),
      `/projects/${c.TENANT_ID}/scf`,
    );
    await waitFor(() => expect(host.textContent).toContain("3 views"));
    await click(byRole(host, "button", "Sign a report"));

    // the last days before today are not final yet
    for (const back of [0, 1, 3]) {
      const day = new Date(Date.now() - back * 86_400_000).toISOString().slice(0, 10);
      await type(byLabel(host, "To") as HTMLInputElement, day);
      await click(byRole(host, "button", "Generate and sign"));
      expect(host.textContent).toContain("The last 4 days can still change.");
    }
    expect(calls.some((x) => x.method === "POST")).toBe(false);

    await type(byLabel(host, "From") as HTMLInputElement, "2026-10-01");
    await type(byLabel(host, "To") as HTMLInputElement, "2026-10-31");
    await click(byRole(host, "button", "Generate and sign"));
    // the link shows in the same dialog that signed it
    await waitFor(() => expect(host.textContent).toContain("Report signed"));
    expect(host.querySelector("dialog [role=status] a")?.getAttribute("href")).toBe(summary.url);
    expect(calls.find((x) => x.method === "POST")?.body).toEqual({
      from: "2026-10-01",
      to: "2026-10-31",
    });
    expect(calls.find((x) => x.method === "POST")?.url).toBe(`/api/tenants/${c.TENANT_ID}/reports`);
    await click(byRole(host, "button", "Done"));

    // one main action per row; the rest is in a menu named for its report
    const row = host.querySelector(`[data-report="${summary.id}"]`) as HTMLElement;
    expect(
      row.querySelector('button[aria-label="Copy link of the report for Oct 1 to Oct 31, 2026"]'),
    ).not.toBeNull();
    await openMenu(row, "Actions for the report for Oct 1 to Oct 31, 2026");
    await click(menuItem("Revoke link"));
    const dialog = [...host.querySelectorAll("dialog")].find((d) => d.hasAttribute("open"));
    const confirm = [...(dialog?.querySelectorAll("button") ?? [])].find(
      (b) => b.textContent === "Revoke link",
    );
    await click(confirm as HTMLElement);
    await waitFor(() => expect(host.textContent).toContain("Link revoked"));
    // a revoked report keeps one action, in its menu: its signed file
    const revokedRow = host.querySelector(`[data-report="${summary.id}"]`) as HTMLElement;
    expect(revokedRow.querySelector('button[aria-label^="Copy link of"]')).toBeNull();
    await openMenu(revokedRow, "Actions for the report for Oct 1 to Oct 31, 2026");
    expect(menuItem("Download JSON")).toBeTruthy();
    expect(
      [...globalThis.document.querySelectorAll("[role=menuitem]")].some((m) =>
        m.textContent?.includes("Revoke link"),
      ),
    ).toBe(false);
    expect(calls.find((x) => x.url.endsWith("/revoke"))?.url).toBe(
      `/api/tenants/${c.TENANT_ID}/reports/abcdefghijklmnopqrstuvwxyz/revoke`,
    );
  });

  it("explains the server's refusal of a period that is not final", async () => {
    routeFetch((method) =>
      method === "POST"
        ? { status: 422, body: c.errorBody("period_not_final") }
        : { status: 200, body: { items: [] } },
    );
    const host = await renderRoutes(
      projectRoutes(<SignedReports />, "scf"),
      `/projects/${c.TENANT_ID}/scf`,
    );
    await click(byRole(host, "button", "Sign a report"));
    await type(byLabel(host, "From") as HTMLInputElement, "2026-10-01");
    await type(byLabel(host, "To") as HTMLInputElement, "2026-10-31");
    await click(byRole(host, "button", "Generate and sign"));
    await waitFor(() =>
      expect(host.textContent).toContain("End the report at least four days before today (UTC)"),
    );
  });
});

describe("revoking a report link", () => {
  it("forgets a refusal when the dialog closes", async () => {
    routeFetch((method) => {
      if (method === "POST") return { status: 429, body: c.errorBody("rate_limited") };
      return { status: 200, body: { items: [summary] } };
    });
    const host = await renderRoutes(
      projectRoutes(<SignedReports />, "scf"),
      `/projects/${c.TENANT_ID}/scf`,
    );
    await waitFor(() => expect(host.textContent).toContain("3 views"));
    const open = () => [...host.querySelectorAll("dialog")].find((d) => d.hasAttribute("open"));
    const actions = "Actions for the report for Oct 1 to Oct 31, 2026";
    await openMenu(host, actions);
    await click(menuItem("Revoke link"));
    const confirm = [...(open()?.querySelectorAll("button") ?? [])].find(
      (b) => b.textContent === "Revoke link",
    );
    await click(confirm as HTMLElement);
    await waitFor(() => expect(open()?.textContent).toContain("Too many requests"));
    await click(byRole(open() as HTMLElement, "button", "Cancel"));
    await waitFor(() => expect(open()).toBeUndefined());
    await openMenu(host, actions);
    await click(menuItem("Revoke link"));
    expect(open()?.textContent).not.toContain("Too many requests");
  });
});

describe("scf review with cohorts (contract 19.3)", () => {
  const body = {
    from: "2026-10-18",
    to: "2026-11-16",
    days: ["2026-11-15", "2026-11-16"],
    in_operator_views: true,
    tenant: f.refA,
    sites: { sites: 1, sites_verified: 1, sites_with_stellar_toml: 0 },
    has_web_data: true,
    has_api_data: false,
    web: {
      pageviews: 10,
      visitors: 5,
      visits: 6,
      bounces: 1,
      totaltime: 10,
      bounce_rate: 0.1,
      avg_visit_duration: 2,
      quarantine_pageviews: 0,
      quarantine_visitors: 0,
      quarantine_share: 0,
      ingest_requests: 10,
      ingest_rejected: 0,
      rejected_share: 0,
      coverage: { script_requests: 0, gpc_requests: 0, unmeasured_share: null },
    },
    api: null,
    onchain: null,
    link_two_way: false,
    web_series: [
      { day: "2026-11-15", aggregated: true, pageviews: 5, visitors: 2, visits: 3 },
      { day: "2026-11-16", aggregated: true, pageviews: 5, visitors: 3, visits: 3 },
    ],
    api_series: [],
    onchain_series: [],
    contract_functions: [],
    cohort: {
      tenant_id: c.TENANT_ID,
      confirmed: { ...f.cohortA, award_date: "2026-11-15" },
      confirmed_at: "2026-11-01T09:00:00Z",
      proposal: { program: "Build Award", round: "SCF #39", category: null, award_date: null },
      proposed_at: "2026-11-10T09:00:00Z",
    },
    percentile_cohort: { program: "Build Award", round: "SCF #38" },
    comparison: { ...f.comparisonA, cohort: { ...f.cohortA, award_date: "2026-11-15" } },
    rules_met: [{ rule, match: matches.items[0] }],
  };

  it("shows the cohort, the change and percentile of each metric and the rules met", async () => {
    const calls = routeFetch((method, url) => {
      if (method === "PUT")
        return {
          status: 200,
          body: {
            ...body.cohort,
            proposal: {
              program: "Build Award",
              round: "SCF #40",
              category: null,
              award_date: null,
            },
          },
        };
      if (url.includes("/integrity")) return { status: 200, body: f.integrityReportBody };
      if (url.includes("/sites")) return { status: 200, body: { items: [], next_cursor: null } };
      if (url.includes("/reports")) return { status: 200, body: { items: [] } };
      return { status: 200, body };
    });
    const host = await renderRoutes(
      projectRoutes(<ScfReviewPage />, "scf"),
      `/projects/${c.TENANT_ID}/scf`,
    );
    await waitFor(() => expect(host.querySelector("[data-testid=verdict]")).not.toBeNull());
    // the verdict: the cohort, the rule met and the key numbers with change
    const verdict = host.querySelector("[data-testid=verdict]");
    expect(verdict?.querySelector("[data-cohort]")?.textContent).toContain("4 projects");
    expect(verdict?.querySelector(`[data-rule-met="${rule.id}"]`)).not.toBeNull();
    expect(verdict?.querySelector("[data-key-number=visitors]")?.textContent).toContain("+100%");
    expect(host.querySelector("[data-testid=cohort-population]")?.textContent).toBe(
      "Build Award, SCF #38 · 4 projects",
    );
    const visitors = host.querySelector("tr[data-kpi=visitors]");
    expect(visitors?.textContent).toContain("+100%");
    // a cohort of four is too small for a percentile: no column of dashes
    expect(visitors?.textContent).not.toContain("P75");
    expect(host.querySelector("[data-percentile]")).toBeNull();
    expect([...host.querySelectorAll("th")].some((th) => th.textContent === "Percentile")).toBe(
      false,
    );
    expect(host.querySelector("tr[data-kpi=error_rate]")?.textContent).toContain("-0.1 pp");
    expect(host.querySelector(`[data-rule="${rule.id}"]`)?.textContent).toContain(
      "Oct 2026: 4 qualifying days",
    );
    // the full integrity report sits on this page, with its appeal for a B
    await waitFor(() =>
      expect(host.querySelector('[aria-label="Integrity report"]')).not.toBeNull(),
    );
    expect(host.querySelector("[data-testid=integrity-appeal]")).not.toBeNull();
    expect(host.querySelectorAll("[data-testid=coverage]")).toHaveLength(1);
    expect(host.querySelector("[data-proposal]")?.textContent).toContain("SCF #39");
    expect(host.querySelector("[data-award]")?.textContent).toContain("award date");
    expect(host.querySelector("[data-marker='2026-11-15']")).not.toBeNull();

    await click(byRole(host, "button", "Edit"));
    await type(byLabel(host, "Round") as HTMLInputElement, "SCF #40");
    await click(byRole(host, "button", "Send proposal"));
    await waitFor(() => expect(calls.find((x) => x.method === "PUT")).toBeDefined());
    expect(calls.find((x) => x.method === "PUT")).toMatchObject({
      url: `/api/tenants/${c.TENANT_ID}/cohort/proposal`,
      body: { program: "Build Award", round: "SCF #40", category: null, award_date: null },
    });
  });
});

describe("enrollment with a cohort proposal", () => {
  it("sends the proposal only when a field is filled", () => {
    const base = {
      name: "x",
      enrollment: { website: "https://example.org", stellar_usage: "x" },
      accepted_terms_version: "1",
    };
    expect(withCohort(base, { program: "", round: " ", category: "", award_date: "" })).toEqual(
      base,
    );
    expect(
      withCohort(base, {
        program: " Build Award ",
        round: "",
        category: "",
        award_date: "2026-10-01",
      }),
    ).toEqual({ ...base, cohort: { program: "Build Award", award_date: "2026-10-01" } });
  });
});

describe("the operator's cohort panel (contract 19.1)", () => {
  it("confirms a proposal and edits the confirmed values", async () => {
    const base = {
      tenant_id: c.TENANT_ID,
      confirmed: null,
      confirmed_at: null,
      proposal: {
        program: "Build Award",
        round: "SCF #38",
        category: null,
        award_date: "2026-10-01",
      },
      proposed_at: "2026-11-10T09:00:00Z",
    };
    const confirmed = {
      ...base,
      confirmed: base.proposal,
      confirmed_at: "2026-11-16T09:00:00Z",
      proposal: null,
      proposed_at: null,
    };
    let state: typeof base | typeof confirmed = base;
    const calls = routeFetch((method, url) => {
      if (method === "POST" && url.endsWith("/cohort/confirm")) state = confirmed;
      if (method === "PUT")
        return {
          status: 200,
          body: { ...confirmed, confirmed: { ...base.proposal, category: "DeFi" } },
        };
      return { status: 200, body: state };
    });
    const host = await renderRoutes(
      [{ path: "/x", element: <OperatorCohortPanel tenantId={c.TENANT_ID} /> }],
      "/x",
    );
    await waitFor(() =>
      expect(host.querySelector("[data-proposal]")?.textContent).toContain("SCF #38"),
    );
    expect(host.textContent).toContain("No cohort is confirmed for this project.");
    await click(byRole(host, "button", "Confirm proposal"));
    await waitFor(() => expect(host.querySelector("[data-proposal]")).toBeNull());
    expect(calls.find((x) => x.method === "POST")?.url).toBe(
      `/api/operator/tenants/${c.TENANT_ID}/cohort/confirm`,
    );
    expect(host.textContent).toContain("Build Award");

    await click(byRole(host, "button", "Edit"));
    await type(byLabel(host, "Category") as HTMLInputElement, "DeFi");
    await type(byLabel(host, "Award date") as HTMLInputElement, "2031-01-01");
    await click(byRole(host, "button", "Save cohort"));
    expect(host.textContent).toContain("A day from 2018 up to today.");
    await type(byLabel(host, "Award date") as HTMLInputElement, "2026-10-01");
    await click(byRole(host, "button", "Save cohort"));
    await waitFor(() => expect(calls.find((x) => x.method === "PUT")).toBeDefined());
    expect(calls.find((x) => x.method === "PUT")?.body).toEqual({
      program: "Build Award",
      round: "SCF #38",
      category: "DeFi",
      award_date: "2026-10-01",
    });
  });
});

// radix opens a menu on pointer down, not on click
async function openMenu(scope: HTMLElement, label: string) {
  const trigger = scope.querySelector<HTMLElement>(`button[aria-label="${label}"]`);
  if (!trigger) throw new Error(`no menu ${label}`);
  await act(async () => {
    trigger.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, button: 0 }));
  });
}

function menuItem(text: string): HTMLElement {
  // the report fixture above is named document; the page is globalThis's
  const el = [...globalThis.document.querySelectorAll<HTMLElement>("[role=menuitem]")].find(
    (m) => m.textContent === text,
  );
  if (!el) throw new Error(`no menu item ${text}`);
  return el;
}
