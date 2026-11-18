// signed usage reports (contract 20): the period rules a report of closed
// utc days keeps, and the signed file as people download it

import { fileJSON, type SignedFile } from "@/api/reports";
import { isDay, lastDays } from "@/lib/global";

export const MAX_REPORT_DAYS = 366;

// a report ends this many days before today at the latest: the daily
// aggregation and grading read today and the three days before it again,
// so only older days are final and a signed number never changes
export const FINAL_DAYS = 4;

// the last final day, as yyyy-mm-dd in utc
export function lastFinalDay(now: Date): string {
  return lastDays(1, new Date(now.getTime() - FINAL_DAYS * 86_400_000)).to;
}

// the last n final days
export function closedDays(n: number, now: Date): { from: string; to: string } {
  return lastDays(n, new Date(now.getTime() - FINAL_DAYS * 86_400_000));
}

// the bounds of 20.3, checked before sending; the server checks them again
export function validatePeriod(
  from: string,
  to: string,
  now: Date,
): { from?: string; to?: string } {
  const out: { from?: string; to?: string } = {};
  if (!isDay(from)) out.from = "Enter a start day as 2026-11-18.";
  if (!isDay(to)) out.to = "Enter an end day as 2026-11-18.";
  if (out.from || out.to) return out;
  const last = lastFinalDay(now);
  if (to > last)
    out.to = `End on ${last} or earlier (UTC). The last ${FINAL_DAYS} days can still change.`;
  else if (to < from) out.to = "End on or after the start.";
  else if ((Date.parse(to) - Date.parse(from)) / 86_400_000 + 1 > MAX_REPORT_DAYS)
    out.to = `A report covers at most ${MAX_REPORT_DAYS} days.`;
  return out;
}

// saves the signed file as the json people attach to a submission
export function downloadFile(file: SignedFile, name: string) {
  const blob = new Blob([fileJSON(file)], { type: "application/json" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  document.body.append(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}
