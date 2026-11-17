// response bodies of contract sections 8 and 9 (docs/reference/control-plane-api.md),
// with the placeholders ("uuid", "...") filled with values of the right shape.
// test fixtures only; product code never imports this file.

import { OPERATOR_ID, TENANT_ID } from "./contract";

export const TENANT_B = "0193a6f3-0202-7a22-b3c4-d5e6f7a8b9c1";
export const TENANT_C = "0193a6f3-0303-7a22-b3c4-d5e6f7a8b9c2";
export const KEY_ID = "0193a6fb-5555-7f77-8a88-b99caadbbecc";
export const KEY_PLAIN = "sa_op_Ab3x9QnRZtK2mW8vLp0sYcD4hJ7uE1fGiO5kNqBa";

export const refA = {
  id: TENANT_ID,
  slug: "stellar-dex",
  name: "Stellar DEX",
  status: "active",
  created_at: "2026-11-01T09:00:00Z",
};
export const refB = {
  id: TENANT_B,
  slug: "soroban-pay",
  name: "Soroban Pay",
  status: "suspended",
  created_at: "2026-11-02T09:00:00Z",
};
export const refC = {
  id: TENANT_C,
  slug: "anchor-hub",
  name: "Anchor Hub",
  status: "active",
  created_at: "2026-11-03T09:00:00Z",
};

// 19.2 a project confirmed in a cohort, and one without a cohort or data
export const cohortA = {
  program: "Build Award",
  round: "SCF #38",
  category: "DeFi",
  award_date: "2026-11-11",
};
export const noCohort = { program: null, round: null, category: null, award_date: null };
const noKpi = { value: null, previous: null, change: null, percentile: null };
export const comparisonA = {
  cohort: cohortA,
  cohort_size: 4,
  kpis: {
    visitors: { value: 410, previous: 205, change: 1, percentile: 0.75 },
    pageviews: { value: 1200, previous: 1500, change: -0.2, percentile: 0.5 },
    requests: { value: 182000, previous: 160000, change: 0.1375, percentile: 1 },
    error_rate: { value: 0.0005, previous: 0.0015, change: -0.001, percentile: 0.25 },
    onchain_operations: { value: 0, previous: 0, change: null, percentile: null },
  },
};
export const comparisonB = {
  cohort: noCohort,
  cohort_size: 4,
  kpis: {
    visitors: { value: 0, previous: 0, change: null, percentile: null },
    pageviews: { value: 0, previous: 0, change: null, percentile: null },
    requests: noKpi,
    error_rate: noKpi,
    onchain_operations: noKpi,
  },
};

// 19.1 the cohort values in use and a pending proposal
export const cohortListBody = {
  programs: [{ value: "Build Award", projects: 3 }],
  rounds: [
    { value: "SCF #37", projects: 1 },
    { value: "SCF #38", projects: 2 },
  ],
  categories: [{ value: "DeFi", projects: 2 }],
  proposals: [],
};

// 8.3, the contract's example item
export const webProjectA = {
  tenant: refA,
  sites: 2,
  sites_verified: 1,
  sites_with_stellar_toml: 1,
  has_web_data: true,
  has_api_data: false,
  pageviews: 1200,
  visitors: 410,
  visits: 530,
  bounces: 260,
  totaltime: 48200,
  bounce_rate: 0.4905660377358491,
  avg_visit_duration: 90.94339622641509,
  quarantine_pageviews: 40,
  quarantine_visitors: 12,
  quarantine_share: 0.03225806451612903,
  ingest_requests: 1890,
  ingest_rejected: 75,
  rejected_share: 0.03968253968253968,
  integrity: { grade: "B", graded_days: 30, days_a: 21, days_b: 9, days_c: 0 },
  coverage: { script_requests: 2400, gpc_requests: 288, unmeasured_share: 0.12 },
  top: {
    referrer: [{ key: "github.com", value: 81 }],
    country: [
      { key: "DE", value: 120 },
      { key: "NG", value: 64 },
    ],
    device: [{ key: "laptop", value: 300 }],
    browser: [{ key: "chrome", value: 250 }],
  },
  ...comparisonA,
};

// a project without visits: every ratio null
export const webProjectB = {
  tenant: refB,
  sites: 1,
  sites_verified: 0,
  sites_with_stellar_toml: 0,
  has_web_data: false,
  has_api_data: true,
  pageviews: 0,
  visitors: 0,
  visits: 0,
  bounces: 0,
  totaltime: 0,
  bounce_rate: null,
  avg_visit_duration: null,
  quarantine_pageviews: 0,
  quarantine_visitors: 0,
  quarantine_share: null,
  ingest_requests: 0,
  ingest_rejected: 0,
  rejected_share: null,
  integrity: { grade: null, graded_days: 0, days_a: 0, days_b: 0, days_c: 0 },
  coverage: { script_requests: 40, gpc_requests: 3, unmeasured_share: null },
  top: { referrer: [], country: [], device: [], browser: [] },
  ...comparisonB,
};

export const webProjectsBody = {
  from: "2026-10-14",
  to: "2026-11-12",
  total: 3,
  items: [webProjectA, webProjectB],
  next_cursor: "cG9zOjI",
};
export const webProjectsPage2Body = {
  from: "2026-10-14",
  to: "2026-11-12",
  total: 3,
  items: [{ ...webProjectA, tenant: refC, visitors: 5 }],
  next_cursor: null,
};

// 8.5, the contract's example item: p99 in the open bucket
export const apiProjectA = {
  tenant: refA,
  sites: 1,
  sites_verified: 1,
  sites_with_stellar_toml: 0,
  has_web_data: false,
  has_api_data: true,
  requests: 182000,
  requests_4xx: 2100,
  requests_5xx: 90,
  rate_4xx: 0.011538461538461539,
  rate_5xx: 0.0004945054945054945,
  p50_seconds: 0.041,
  p50_over_10s: false,
  p95_seconds: 0.62,
  p95_over_10s: false,
  p99_seconds: null,
  p99_over_10s: true,
  infrastructure_requests: 0,
  services: 3,
  routes: 41,
  span_services: 1,
  source: "mixed",
  lossy: false,
  ...comparisonA,
};
export const apiProjectB = {
  ...apiProjectA,
  ...comparisonB,
  tenant: refB,
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
  span_services: 0,
  source: null,
};
export const apiProjectsBody = {
  from: "2026-10-14",
  to: "2026-11-12",
  total: 2,
  items: [apiProjectA, apiProjectB],
  next_cursor: null,
};

const DAYS = ["2026-11-10", "2026-11-11", "2026-11-12"];

const emptyBreakdowns = {
  referrer: { items: [], total_keys: 0 },
  country: { items: [], total_keys: 0 },
  device: { items: [], total_keys: 0 },
  browser: { items: [], total_keys: 0 },
  os: { items: [], total_keys: 0 },
};

// 8.4: the first day of B was never read (aggregated false, numbers null)
export const webCompareBody = {
  from: "2026-11-10",
  to: "2026-11-12",
  days: DAYS,
  projects: [
    {
      tenant: refA,
      cohort: cohortA,
      coverage: { script_requests: 500, gpc_requests: 60, unmeasured_share: 0.12 },
      integrity: {
        grade: "C",
        graded_days: 3,
        days_a: 1,
        days_b: 0,
        days_c: 2,
        reasons: [
          {
            code: "hosting_share",
            level: "C",
            days: 2,
            text: "A large share of events came from hosting networks or anonymisers.",
          },
        ],
      },
      totals: {
        pageviews: 45,
        visitors: 18,
        visits: 20,
        bounces: 8,
        totaltime: 1800,
        bounce_rate: 0.4,
        avg_visit_duration: 90,
        quarantine_pageviews: 5,
        quarantine_visitors: 1,
        quarantine_share: 0.1,
      },
      series: DAYS.map((day, i) => ({
        day,
        aggregated: true,
        pageviews: 15,
        visitors: 6,
        visits: i === 0 ? 7 : 6,
        bounces: i === 0 ? 4 : 2,
        totaltime: 600,
        bounce_rate: i === 0 ? 4 / 7 : 1 / 3,
        avg_visit_duration: i === 0 ? 600 / 7 : 100,
        quarantine_pageviews: i === 0 ? 5 : 0,
      })),
      breakdowns: {
        ...emptyBreakdowns,
        referrer: { items: [{ key: "github.com", value: 9 }], total_keys: 14 },
        country: { items: [{ key: "DE", value: 12 }], total_keys: 1 },
      },
    },
    {
      tenant: refB,
      cohort: noCohort,
      coverage: { script_requests: 500, gpc_requests: 60, unmeasured_share: 0.12 },
      integrity: { grade: null, graded_days: 0, days_a: 0, days_b: 0, days_c: 0, reasons: [] },
      totals: {
        pageviews: 0,
        visitors: 0,
        visits: 0,
        bounces: 0,
        totaltime: 0,
        bounce_rate: null,
        avg_visit_duration: null,
        quarantine_pageviews: 0,
        quarantine_visitors: 0,
        quarantine_share: null,
      },
      series: DAYS.map((day, i) =>
        i === 0
          ? {
              day,
              aggregated: false,
              pageviews: null,
              visitors: null,
              visits: null,
              bounces: null,
              totaltime: null,
              bounce_rate: null,
              avg_visit_duration: null,
              quarantine_pageviews: null,
            }
          : {
              day,
              aggregated: true,
              pageviews: 0,
              visitors: 0,
              visits: 0,
              bounces: 0,
              totaltime: 0,
              bounce_rate: null,
              avg_visit_duration: null,
              quarantine_pageviews: 0,
            },
      ),
      breakdowns: emptyBreakdowns,
    },
  ],
};

const noRequests = {
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
};

const someRequests = {
  requests: 100,
  requests_4xx: 5,
  requests_5xx: 1,
  rate_4xx: 0.05,
  rate_5xx: 0.01,
  p50_seconds: 0.02,
  p50_over_10s: false,
  p95_seconds: 0.4,
  p95_over_10s: false,
  p99_seconds: null,
  p99_over_10s: true,
  infrastructure_requests: 0,
};

// 8.6
export const apiCompareBody = {
  from: "2026-11-10",
  to: "2026-11-12",
  days: DAYS,
  projects: [
    {
      tenant: refA,
      cohort: cohortA,
      totals: {
        ...someRequests,
        requests: 300,
        requests_4xx: 15,
        requests_5xx: 3,
        services: 2,
        routes: 7,
        span_services: 1,
        source: "mixed",
        lossy: false,
      },
      series: DAYS.map((day) => ({ day, ...someRequests })),
      services: [
        {
          service: "payments-api",
          source: "metric",
          ...someRequests,
          requests: 250,
          routes: 5,
          lossy: false,
        },
        {
          service: "ledger-worker",
          source: "span",
          ...someRequests,
          requests: 50,
          routes: 2,
          lossy: false,
        },
      ],
    },
    {
      tenant: refB,
      cohort: noCohort,
      totals: {
        ...noRequests,
        services: 0,
        routes: 0,
        span_services: 0,
        source: null,
        lossy: false,
      },
      series: DAYS.map((day) => ({ day, ...noRequests })),
      services: [],
    },
  ],
};

// 9.2
export const apiKeyBody = {
  id: KEY_ID,
  name: "warehouse",
  prefix: "sa_op_Ab3x",
  scope: "metrics:read",
  tenant_id: null,
  status: "active",
  created_by: OPERATOR_ID,
  created_at: "2026-11-12T09:00:00Z",
  expires_at: "2027-02-10T09:00:00Z",
  revoked_at: null,
  last_used_at: null,
};
export const apiKeyCreatedBody = { ...apiKeyBody, key: KEY_PLAIN };
export const apiKeyPageBody = { items: [apiKeyBody], next_cursor: null };
export const revokedKeyBody = {
  ...apiKeyBody,
  status: "revoked",
  revoked_at: "2026-11-12T10:00:00Z",
  last_used_at: "2026-11-12T09:30:00Z",
};

// 9.11
export const apiKeyUsageBody = {
  key_id: KEY_ID,
  from: "2026-11-06",
  to: "2026-11-12",
  totals: { requests: 12, rejected: 2 },
  hours: [
    { hour: "2026-11-12T09:00:00Z", requests: 10, rejected: 0 },
    { hour: "2026-11-12T10:00:00Z", requests: 2, rejected: 2 },
  ],
};

// 17.3 one project's integrity report: one site, a clean closed day and a
// day in progress lowered to B
export const integrityReportBody = {
  tenant_id: TENANT_ID,
  from: "2026-11-11",
  to: "2026-11-12",
  rule_version: 2,
  rule_versions: [1],
  coverage: { script_requests: 300, gpc_requests: 36, unmeasured_share: 0.12 },
  grade: "B",
  graded_days: 2,
  days_a: 1,
  days_b: 1,
  days_c: 0,
  reasons: [
    {
      code: "quarantine_share",
      level: "B",
      days: 1,
      text: "A large share of accepted events went to the quarantine band.",
    },
  ],
  sites: [
    {
      site_id: "0193a6f4-0101-7a22-b3c4-d5e6f7a8b9c0",
      coverage: { script_requests: 300, gpc_requests: 36, unmeasured_share: 0.12 },
      grade: "B",
      graded_days: 2,
      days_a: 1,
      days_b: 1,
      days_c: 0,
      reasons: [
        {
          code: "quarantine_share",
          level: "B",
          days: 1,
          text: "A large share of accepted events went to the quarantine band.",
        },
      ],
      days: [
        {
          day: "2026-11-11",
          grade: "A",
          closed: true,
          rule_version: 1,
          reasons: [],
          evidence: {
            events: 1000,
            quarantined: 0,
            proof_failures: 10,
            networks: 120,
            visitors: 400,
            pageviews: 1000,
            baseline_visitors: 380,
            baseline_days: 28,
            net: { access: 850, organisation: 60, hosting: 80, anonymiser: 0, unknown: 10 },
            hosting_share: 0.08,
            quarantine_share: 0,
            proof_pass_rate: 0.9900990099009901,
          },
          coverage: {
            script_requests: 180,
            gpc_requests: 20,
            unmeasured_share: 0.1111111111111111,
          },
        },
        {
          day: "2026-11-12",
          grade: "B",
          closed: false,
          rule_version: 1,
          reasons: [
            {
              code: "quarantine_share",
              level: "B",
              value: 0.15,
              limit: 0.1,
              text: "15% of accepted events went to the quarantine band (limit 10%): crowded address prefixes or visitors past the hourly volume bound.",
            },
          ],
          evidence: {
            events: 200,
            quarantined: 30,
            proof_failures: 0,
            networks: 40,
            visitors: 90,
            pageviews: 180,
            baseline_visitors: null,
            baseline_days: 0,
            net: { access: 0, organisation: 0, hosting: 0, anonymiser: 0, unknown: 0 },
            hosting_share: null,
            quarantine_share: 0.15,
            proof_pass_rate: 1,
          },
          coverage: {
            script_requests: 120,
            gpc_requests: 16,
            unmeasured_share: 0.13333333333333333,
          },
        },
      ],
    },
  ],
};
