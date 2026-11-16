// the on-chain schemas that schemas.ts also needs. this module imports
// only zod, so schemas.ts can import it without a cycle through client.ts

import { z } from "zod";

z.config({ jitless: true });

const day = z.string().regex(/^\d{4}-\d{2}-\d{2}$/);
const count = z.number().int().nonnegative();

// the on-chain column of the operator lists and the compare summary
export const onchainSummary = z.object({
  ids: count,
  ids_verified: count,
  operations: count,
  transactions: count,
  days_missing: count,
  ids_failing: count,
});
export type OnchainSummary = z.infer<typeof onchainSummary>;

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
});
export type CompareOnchain = z.infer<typeof compareOnchain>;
