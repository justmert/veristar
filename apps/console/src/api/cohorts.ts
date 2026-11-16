// cohorts and saved threshold rules (control plane contract section 19):
// schemas, calls and query options in one module, like onchain.ts

import { queryOptions } from "@tanstack/react-query";
import { z } from "zod";
import { request, requestNoContent } from "./client";
import { cohort } from "./cohort-schemas";

export { type Cohort, cohort, type Kpi, type KpiKey, type Kpis, kpi, kpis } from "./cohort-schemas";

z.config({ jitless: true });

const enc = encodeURIComponent;
const day = z.string().regex(/^\d{4}-\d{2}-\d{2}$/);
const month = z.string().regex(/^\d{4}-\d{2}$/);
const time = z.string();
const count = z.number().int().nonnegative();
const id = z.uuid();
const grade = z.enum(["A", "B", "C"]);

// 19.1 a project's cohort row: the confirmed values and the pending proposal
export const cohortRecord = z.object({
  tenant_id: id,
  confirmed: cohort.nullable(),
  confirmed_at: time.nullable(),
  proposal: cohort.nullable(),
  proposed_at: time.nullable(),
});
export type CohortRecord = z.infer<typeof cohortRecord>;

const tenantRef = z.object({
  id,
  slug: z.string(),
  name: z.string(),
  status: z.string(),
  created_at: time,
});

export const cohortValue = z.object({ value: z.string(), projects: count });
export type CohortValue = z.infer<typeof cohortValue>;

// 19.1 GET /api/operator/cohorts
export const cohortList = z.object({
  programs: z.array(cohortValue),
  rounds: z.array(cohortValue),
  categories: z.array(cohortValue),
  proposals: z.array(
    z.object({
      tenant: tenantRef,
      confirmed: cohort.nullable(),
      proposal: cohort,
      proposed_at: time,
    }),
  ),
});
export type CohortList = z.infer<typeof cohortList>;

// the body of a cohort change: a null or absent field is not set
export type CohortInput = {
  program?: string | null;
  round?: string | null;
  category?: string | null;
  award_date?: string | null;
};

// 19.4 a saved threshold rule
export const ruleMetric = z.enum(["visitors", "requests", "onchain_operations"]);
export type RuleMetric = z.infer<typeof ruleMetric>;
export const rule = z.object({
  id,
  name: z.string(),
  metric: ruleMetric,
  min_per_day: z.number().int().positive(),
  min_days: z.number().int().min(1).max(28),
  months: z.number().int().min(1).max(12),
  min_grade: grade.nullable(),
  cohort: z.object({
    program: z.string().nullable(),
    round: z.string().nullable(),
    category: z.string().nullable(),
  }),
  created_at: time,
  updated_at: time,
});
export type Rule = z.infer<typeof rule>;
export const ruleList = z.object({ items: z.array(rule) });

export type RuleInput = {
  name: string;
  metric: RuleMetric;
  min_per_day: number;
  min_days: number;
  months: number;
  min_grade: "A" | "B" | "C" | null;
  cohort: { program: string | null; round: string | null; category: string | null };
};

export const ruleMatch = z.object({
  tenant: tenantRef,
  cohort,
  meets: z.boolean(),
  integrity: z.object({
    grade: grade.nullable(),
    graded_days: count,
    days_a: count,
    days_b: count,
    days_c: count,
  }),
  months: z.array(
    z.object({
      month,
      qualifying_days: count,
      days: z.array(z.object({ day, value: count })),
    }),
  ),
});
export type RuleMatch = z.infer<typeof ruleMatch>;

export const ruleMatches = z.object({
  rule,
  as_of: day,
  from: day,
  to: day,
  months: z.array(month),
  evaluated: count,
  items: z.array(ruleMatch),
});
export type RuleMatches = z.infer<typeof ruleMatches>;

export const cohortApi = {
  list: (signal?: AbortSignal) =>
    request("GET", "/operator/cohorts", cohortList, undefined, signal),
  operator: (tenantId: string, signal?: AbortSignal) =>
    request("GET", `/operator/tenants/${enc(tenantId)}/cohort`, cohortRecord, undefined, signal),
  set: (tenantId: string, body: CohortInput) =>
    request("PUT", `/operator/tenants/${enc(tenantId)}/cohort`, cohortRecord, body),
  confirm: (tenantId: string) =>
    request("POST", `/operator/tenants/${enc(tenantId)}/cohort/confirm`, cohortRecord),
  dismiss: (tenantId: string) =>
    request("DELETE", `/operator/tenants/${enc(tenantId)}/cohort/proposal`, cohortRecord),
  // the member's own project
  own: (tenantId: string, signal?: AbortSignal) =>
    request("GET", `/tenants/${enc(tenantId)}/cohort`, cohortRecord, undefined, signal),
  propose: (tenantId: string, body: CohortInput) =>
    request("PUT", `/tenants/${enc(tenantId)}/cohort/proposal`, cohortRecord, body),
};

export const ruleApi = {
  list: (signal?: AbortSignal) => request("GET", "/operator/rules", ruleList, undefined, signal),
  create: (body: RuleInput) => request("POST", "/operator/rules", rule, body),
  update: (ruleId: string, body: RuleInput) =>
    request("PUT", `/operator/rules/${enc(ruleId)}`, rule, body),
  remove: (ruleId: string) => requestNoContent("DELETE", `/operator/rules/${enc(ruleId)}`),
  matches: (ruleId: string, asOf: string, signal?: AbortSignal) =>
    request(
      "GET",
      `/operator/rules/${enc(ruleId)}/matches${asOf ? `?as_of=${enc(asOf)}` : ""}`,
      ruleMatches,
      undefined,
      signal,
    ),
};

// csv exports are plain links, like the list exports
export function ruleMatchesCsv(ruleId: string, asOf: string): string {
  return `/api/operator/rules/${enc(ruleId)}/matches.csv${asOf ? `?as_of=${enc(asOf)}` : ""}`;
}

export const cohortKeys = {
  // under the operator prefix, so an operator action refreshes them too
  list: ["operator", "cohorts"] as const,
  operator: (tenantId: string) => ["operator", "cohort", tenantId] as const,
  // under the tenant prefix, so every tenant invalidation refreshes it
  own: (tenantId: string) => ["tenant", tenantId, "cohort"] as const,
  rules: ["operator", "rules"] as const,
  matches: (ruleId: string, asOf: string) =>
    ["operator", "rules", ruleId, "matches", asOf] as const,
};

export const cohortListQuery = queryOptions({
  queryKey: cohortKeys.list,
  queryFn: ({ signal }) => cohortApi.list(signal),
  staleTime: 60_000,
});

export const operatorCohortQuery = (tenantId: string) =>
  queryOptions({
    queryKey: cohortKeys.operator(tenantId),
    queryFn: ({ signal }) => cohortApi.operator(tenantId, signal),
  });

export const ownCohortQuery = (tenantId: string) =>
  queryOptions({
    queryKey: cohortKeys.own(tenantId),
    queryFn: ({ signal }) => cohortApi.own(tenantId, signal),
  });

export const rulesQuery = queryOptions({
  queryKey: cohortKeys.rules,
  queryFn: ({ signal }) => ruleApi.list(signal),
});

export const ruleMatchesQuery = (ruleId: string, asOf: string) =>
  queryOptions({
    queryKey: cohortKeys.matches(ruleId, asOf),
    queryFn: ({ signal }) => ruleApi.matches(ruleId, asOf, signal),
  });
