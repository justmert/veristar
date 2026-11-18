// the on-chain schemas that schemas.ts also needs. this module imports
// only zod, so schemas.ts can import it without a cycle through client.ts

import { z } from "zod";

z.config({ jitless: true });

const day = z.string().regex(/^\d{4}-\d{2}-\d{2}$/);
const count = z.number().int().nonnegative();

// weekly active accounts over the iso weeks of a range: the mean of the
// counted weeks, the latest counted week, and how many are missing
export const weeklyActive = z.object({
  average: z.number().nonnegative().nullable(),
  latest: count.nullable(),
  weeks_counted: count,
  weeks_missing: count,
});
export type WeeklyActive = z.infer<typeof weeklyActive>;

// one iso week; a week without a count carries the reason, never a zero
export const onchainWeek = z.object({
  week: z.string().regex(/^\d{4}-W\d{2}$/),
  from: day,
  to: day,
  active_accounts: count.nullable(),
  missing: z.string().nullable(),
});
export type OnchainWeek = z.infer<typeof onchainWeek>;

// the on-chain column of the operator lists and the compare summary
export const onchainSummary = z.object({
  ids: count,
  ids_verified: count,
  operations: count,
  transactions: count,
  days_missing: count,
  ids_failing: count,
  weekly_active_accounts: weeklyActive,
});
export type OnchainSummary = z.infer<typeof onchainSummary>;

// why operations, transactions and days_missing measured nothing
export const countsReason = z.enum(["no_verified_id", "nothing_read"]);

// the on-chain summary of "what scf sees": the counts are null with
// counts_reason when they measured nothing, never a zero
export const scfOnchainSummary = onchainSummary.extend({
  operations: count.nullable(),
  transactions: count.nullable(),
  days_missing: count.nullable(),
  counts_reason: countsReason.nullable(),
});
export type SCFOnchainSummary = z.infer<typeof scfOnchainSummary>;

export const onchainDayTotal = z.object({
  day,
  ids_read: count,
  operations: count.nullable(),
  transactions: count.nullable(),
});
export type OnchainDayTotal = z.infer<typeof onchainDayTotal>;

export const compareOnchain = z.object({
  totals: onchainSummary,
  series: z.array(onchainDayTotal),
  weeks: z.array(onchainWeek),
});
export type CompareOnchain = z.infer<typeof compareOnchain>;
