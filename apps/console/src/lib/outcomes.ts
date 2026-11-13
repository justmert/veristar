// outcome names stored in cp.web_ingest_hourly.outcome (contract 4.3.7,
// internal/events outcomeRe, internal/gateway/reasons.go). reasons the console
// does not know yet are summed under "other rejections", as the contract asks.

export const PRIMARY_OUTCOMES = ["accepted", "quarantined", "bot", "dlq"] as const;

const LABELS: Record<string, string> = {
  accepted: "Accepted",
  quarantined: "Quarantined",
  bot: "Bots",
  dlq: "Not delivered",
  rejected_body_too_large: "Body too large",
  rejected_malformed: "Malformed",
  rejected_missing_proof: "Missing proof",
  rejected_unknown_site: "Unknown site",
  rejected_inactive_site: "Site not active",
  rejected_gpc: "Global Privacy Control",
  rejected_origin_missing: "No origin",
  rejected_origin: "Origin not allowed",
  rejected_challenge_tag: "Bad challenge",
  rejected_challenge_expired: "Expired challenge",
  rejected_challenge_future: "Challenge from the future",
  rejected_proof_work: "Proof of work failed",
  rejected_replay: "Replayed",
  rejected_signals: "Browser signals failed",
  rejected_rate_limited: "Rate limited",
  rejected_unavailable: "Queue unavailable",
  rejected_revoked_version: "Revoked tracker version",
};

const HINTS: Record<string, string> = {
  accepted: "Stored in the primary web dashboard.",
  quarantined:
    "Many visitors from one network, or very many events from one visitor. Kept apart in the quarantine view.",
  bot: "Dropped by Umami's bot check.",
  dlq: "Could not be written to the dashboard.",
  rejected_origin: "The page's origin is not in the site's allowed origins.",
  rejected_gpc: "The visitor's browser asked not to be tracked.",
  rejected_revoked_version:
    "The page runs a tracker version that was revoked. Update the snippet to a current version.",
};

export function outcomeLabel(outcome: string): string {
  return LABELS[outcome] ?? "Other rejections";
}

export function outcomeHint(outcome: string): string | null {
  return HINTS[outcome] ?? null;
}

export type RejectionRow = { key: string; label: string; count: number };

// rejected_* totals, known reasons by name, unknown ones summed into one row,
// largest first
export function rejectionRows(totals: Record<string, number>): RejectionRow[] {
  const rows = new Map<string, RejectionRow>();
  for (const [key, count] of Object.entries(totals)) {
    if (!key.startsWith("rejected_") || count === 0) continue;
    const known = key in LABELS;
    const rowKey = known ? key : "other";
    const prev = rows.get(rowKey);
    if (prev) prev.count += count;
    else rows.set(rowKey, { key: rowKey, label: outcomeLabel(key), count });
  }
  return [...rows.values()].sort((a, b) => b.count - a.count || a.label.localeCompare(b.label));
}

export function sumRejected(totals: Record<string, number>): number {
  let n = 0;
  for (const [key, count] of Object.entries(totals)) if (key.startsWith("rejected_")) n += count;
  return n;
}

// every outcome that is not primary and not a rejection (a future outcome
// class); shown so that a total never silently loses events
export function sumUnclassified(totals: Record<string, number>): number {
  let n = 0;
  for (const [key, count] of Object.entries(totals)) {
    if (key.startsWith("rejected_")) continue;
    if ((PRIMARY_OUTCOMES as readonly string[]).includes(key)) continue;
    n += count;
  }
  return n;
}
