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
  not_verified: "The contract is not verified; only verified contracts count",
  not_a_contract: "An account has no contract functions",
  no_call_tree:
    "The ledger API sent no call tree for a transaction; only the calls of accounts are counted",
};

export function missingText(code: string): string {
  return MISSING[code] ?? `Missing (${code})`;
}

const VERIFY_ERRORS: Record<string, string> = {
  no_challenge: "Create a challenge first.",
  challenge_expired: "The challenge expired. Create a new one.",
  bad_signature: "The signature does not match this account and challenge.",
  master_key_no_weight:
    "The signature is valid, but this account's own key cannot send a payment on its own (its weight is 0 or below the medium threshold). Prove the account with a data entry instead.",
  data_entry_mismatch: "The account has no data entry with the challenge value yet.",
  no_verified_owner: "Verify one of the project's accounts first; a contract is proven through it.",
  deployer_not_verified: "The contract was deployed by an account the project has not verified.",
  contract_not_created_in_transaction: "That transaction did not create this contract.",
  contract_created_by_contract:
    "That transaction called a contract, which may have created this one as a factory. A contract created that way cannot be proven by its transaction; use the admin check.",
  verified_through_removed_id:
    "This contract was proven through an id the project removed. Prove it again through a verified account.",
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

// one definition of "Days without data" for every page that shows it
export const DAYS_WITHOUT_DATA =
  "Days before today without a ledger read, added up over the verified ids.";

export const NO_VERIFIED_IDS = "No verified account or contract";
export const NO_LEDGER_READ = "No ledger read for a verified id in this range";

// one missing-value rule for the on-chain figures of every page and the
// signed report. the figures count verified ids only, so without one they
// are not measured, and without a single read day they are not measured
// either. 0 shows only when a read found no activity. series holds the
// verified ids' daily sums, null on a day without a read.
export function chainGap(verified: number, series: { operations: number | null }[]): string | null {
  if (verified === 0) return NO_VERIFIED_IDS;
  if (!series.some((p) => p.operations !== null)) return NO_LEDGER_READ;
  return null;
}

// the activity of a range as scf reviewers count it: verified ids only, and
// days before today. the on-chain view sums every declared id, today
// included; these figures read the same as the scf review and the operator
// views (contract 15.7), so one label never has two definitions.
export type VerifiedActivity = {
  verified: number;
  operations: number;
  transactions: number;
  // verified id-days of the range before today without a stored row
  daysMissing: number;
  // why operations and transactions are not measured, null when they are
  gap: string | null;
  // one point per day: the sum of the verified ids that have a row, or
  // missing with a reason when none has one
  daily: { day: string; value: number | null; missing: string | null }[];
};

export function verifiedActivity(v: OnchainView): VerifiedActivity {
  const ids = v.ids.filter((id) => id.verification.status === "verified");
  let operations = 0;
  let transactions = 0;
  let daysMissing = 0;
  for (const id of ids) {
    for (const p of id.series) {
      if (p.operations !== null) operations += p.operations;
      if (p.transactions !== null) transactions += p.transactions;
      if (p.operations === null && p.missing !== "day_not_complete") daysMissing++;
    }
  }
  const daily = v.days.map((day, i) => {
    let sum: number | null = null;
    let missing: string | null = ids.length === 0 ? "no_verified_ids" : null;
    for (const id of ids) {
      const p = id.series[i];
      if (p?.operations !== null && p?.operations !== undefined) sum = (sum ?? 0) + p.operations;
      else if (p?.missing && !missing) missing = p.missing;
    }
    return { day, value: sum, missing: sum === null ? missing : null };
  });
  return {
    verified: ids.length,
    operations,
    transactions,
    daysMissing,
    gap: chainGap(
      ids.length,
      daily.map((d) => ({ operations: d.value })),
    ),
    daily,
  };
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
