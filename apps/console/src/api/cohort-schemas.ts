// the cohort, growth and percentile schemas (control plane contract section
// 19) that schemas.ts, onchain.ts and cohorts.ts share. this module imports
// only zod, so schemas.ts can import it without a cycle through client.ts

import { z } from "zod";

z.config({ jitless: true });

const day = z.string().regex(/^\d{4}-\d{2}-\d{2}$/);
const count = z.number().int().nonnegative();
const cohortText = z.string().min(1).max(80);

// 19.2 one key metric: its value over the range, over the previous range of
// equal length, the change (relative for counts, absolute for the error
// rate) and the percentile inside the cohort, 0 to 1
export const kpi = z.object({
  value: z.number().nullable(),
  previous: z.number().nullable(),
  change: z.number().nullable(),
  percentile: z.number().min(0).max(1).nullable(),
});
export type Kpi = z.infer<typeof kpi>;

export const kpis = z.object({
  visitors: kpi,
  pageviews: kpi,
  requests: kpi,
  error_rate: kpi,
  onchain_operations: kpi,
});
export type Kpis = z.infer<typeof kpis>;
export type KpiKey = keyof Kpis;

// a project's cohort as an operator confirmed it; every field null without one
export const cohort = z.object({
  program: cohortText.nullable(),
  round: cohortText.nullable(),
  category: cohortText.nullable(),
  award_date: day.nullable(),
});
export type Cohort = z.infer<typeof cohort>;

// what every global list row adds (19.2)
export const comparisonFields = {
  cohort,
  cohort_size: count,
  kpis,
};
