// wording of the on-chain witness: why a day is missing, what a proof
// attempt found, and how each proof works. the codes are the control
// plane's (contract 15), so the console never guesses a cause.

import type { ChainId, ChainVerifyMethod, OnchainView, WeeklyActive } from "@/api/onchain";
import { formatCount } from "@/lib/format";

const MISSING: Record<string, string> = {
  day_not_complete: "The UTC day is not over yet",
  before_backfill_window: "Before the first day read for this account",
  before_rpc_retention: "Older than the events Stellar RPC keeps (about seven days)",
  not_read_yet: "Not read yet; the daily job reads it within the hour",
  rate_limited: "The ledger API refused more requests; the next run retries",
  source_unavailable: "The ledger API did not answer; the next run retries",
  bad_response: "The ledger API answered something unreadable",
  before_source_history: "Older than the history the ledger API keeps",
  day_over_read_cap:
    "More activity than one run reads; the day is left out rather than undercounted",
  account_not_found: "The account does not exist on the ledger",
  not_found: "The ledger API does not know this id",
  week_not_complete: "The ISO week is not over yet",
  week_not_counted: "Weekly active accounts were not counted for this week",
  no_verified_ids:
    "The project had no verified account or contract; only verified ones count toward weekly active accounts",
};

export function missingText(code: string): string {
  return MISSING[code] ?? `Missing (${code})`;
}

const VERIFY_ERRORS: Record<string, string> = {
  no_challenge: "Create a challenge first.",
  challenge_expired: "The challenge expired. Create a new one.",
  bad_signature: "The signature does not match this account and challenge.",
  data_entry_mismatch: "The account has no data entry with the challenge value yet.",
  no_verified_owner: "Verify one of the project's accounts first; a contract is proven through it.",
  deployer_not_verified: "The contract was deployed by an account the project has not verified.",
  contract_not_created_in_transaction: "That transaction did not create this contract.",
  transaction_failed: "That transaction failed on the ledger.",
  no_admin_entry: "The contract stores no Admin or Owner entry that can be read.",
  admin_not_verified: "The contract's admin is not one of the project's verified accounts.",
  not_found: "The ledger API does not know this transaction or contract.",
  account_not_found: "The account does not exist on the ledger.",
  rate_limited: "The ledger API is busy. Try again in a minute.",
  source_unavailable: "The ledger API did not answer. Try again in a minute.",
  before_source_history: "The transaction is older than the history the ledger API keeps.",
  bad_response: "The ledger API answered something unreadable.",
};

export function verifyErrorText(code: string): string {
  return VERIFY_ERRORS[code] ?? `The check failed (${code}).`;
}

export const METHOD_TEXT: Record<ChainVerifyMethod, string> = {
  sep53: "Signed message (SEP-53)",
  manage_data: "Account data entry",
  contract_admin: "Contract admin",
  contract_deployer: "Contract deployer",
};

export function shortKey(s: string): string {
  return s.length > 12 ? `${s.slice(0, 4)}…${s.slice(-4)}` : s;
}

// the reasons an id has days without numbers in the range, with how many
// days each covers, most frequent first
export function missingReasons(id: ChainId): { code: string; days: number }[] {
  const by = new Map<string, number>();
  for (const p of id.series) {
    if (p.missing && p.missing !== "day_not_complete")
      by.set(p.missing, (by.get(p.missing) ?? 0) + 1);
  }
  return [...by.entries()].map(([code, days]) => ({ code, days })).sort((a, b) => b.days - a.days);
}

// the project's daily sum over its ids; a day no id has a row for is null
export function dailyOperations(v: OnchainView): { day: string; value: number | null }[] {
  return v.days.map((day, i) => {
    let sum: number | null = null;
    for (const id of v.ids) {
      const n = id.series[i]?.operations;
      if (n !== null && n !== undefined) sum = (sum ?? 0) + n;
    }
    return { day, value: sum };
  });
}

// the weekly active accounts of a range in one line: the scf growth hack
// averages the weekly counts, so the mean leads and the latest week follows
export function weeklyActiveText(w: WeeklyActive): string {
  if (w.average === null) {
    return w.weeks_missing > 0
      ? `No complete week counted (${w.weeks_missing} missing)`
      : "No complete ISO week in this range";
  }
  const avg = formatCount(Math.round(w.average));
  const weeks = w.weeks_counted === 1 ? "1 week" : `${w.weeks_counted} weeks`;
  const missing = w.weeks_missing ? `, ${w.weeks_missing} missing` : "";
  return `${avg} a week on average over ${weeks}${missing}`;
}
