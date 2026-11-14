// helpers of the operator alerts page (contract 11): severity groups, how
// long an alert has fired, and the runbook link of an alert.

import type { FiringAlert } from "@/api/schemas";

// the severities of the alert rules (infra/ansible/roles/observability/
// files/rules): page means act now, ticket within a working day. page is
// the critical one, which the banner on every operator page counts.
const KNOWN: Record<string, { label: string; hint: string; word: string; rank: number }> = {
  page: { label: "Page", hint: "act now", word: "Act now", rank: 0 },
  ticket: {
    label: "Ticket",
    hint: "act within a working day",
    word: "Within a working day",
    rank: 1,
  },
};

export const CRITICAL_SEVERITY = "page";

export type SeverityGroup = {
  key: string;
  label: string;
  hint: string | null;
  alerts: FiringAlert[];
};

// groups keep the control plane's order inside each group (longest firing
// first); a severity the rules do not use yet gets its own group after the
// known ones, and alerts without a severity come last
export function groupBySeverity(alerts: FiringAlert[]): SeverityGroup[] {
  const groups = new Map<string, SeverityGroup>();
  for (const a of alerts) {
    const key = a.severity ?? "";
    let g = groups.get(key);
    if (!g) {
      const known = KNOWN[key];
      g = {
        key,
        label: known?.label ?? (key === "" ? "No severity" : key),
        hint: known?.hint ?? null,
        alerts: [],
      };
      groups.set(key, g);
    }
    g.alerts.push(a);
  }
  const rank = (key: string) => KNOWN[key]?.rank ?? (key === "" ? 3 : 2);
  return [...groups.values()].sort(
    (x, y) => rank(x.key) - rank(y.key) || x.key.localeCompare(y.key),
  );
}

// how an alert item says its urgency: the severity in words, with the tone
// of its icon. a held back or not yet processed alert says that instead,
// because its severity is not what the operator acts on
export type AlertTone = "critical" | "warning" | "muted";

export function alertUrgency(a: FiringAlert): { word: string; tone: AlertTone } {
  if (a.state === "suppressed")
    return { word: a.inhibited_by.length > 0 ? "Inhibited" : "Suppressed", tone: "muted" };
  if (a.state === "unprocessed") return { word: "Just received", tone: "muted" };
  if (a.severity === CRITICAL_SEVERITY) return { word: KNOWN.page?.word ?? "", tone: "critical" };
  const known = a.severity ? KNOWN[a.severity] : undefined;
  if (known) return { word: known.word, tone: "warning" };
  return { word: a.severity ?? "No severity", tone: "warning" };
}

export function criticalCount(alerts: FiringAlert[]): number {
  return alerts.filter((a) => a.severity === CRITICAL_SEVERITY).length;
}

// "4 min", "2 h 5 min", "3 d 4 h". under a minute is said as such, and a
// start in the future (clock skew between the browser and alertmanager) as
// just started, never as a negative time
export function firingFor(startsAt: string, now: number): string {
  const ms = now - new Date(startsAt).getTime();
  const min = Math.floor(ms / 60_000);
  if (min < 1) return "less than a minute";
  if (min < 60) return `${min} min`;
  const h = Math.floor(min / 60);
  if (h < 24) return min % 60 === 0 ? `${h} h` : `${h} h ${min % 60} min`;
  const d = Math.floor(h / 24);
  return h % 24 === 0 ? `${d} d` : `${d} d ${h % 24} h`;
}

// the runbooks are part of the repository; an alert's runbook annotation is
// a path in it with the anchor of the alert's section (tests/docs
// alerts_test.go keeps every anchor resolving)
export const REPOSITORY_BLOB = "https://github.com/justmert/veristar/blob/main/";
const RUNBOOK_PATH = /^docs\/runbooks\/[a-z0-9-]+\.md#[a-z0-9-]+$/;

// only a path of the runbooks folder becomes a link; anything else in the
// annotation is shown as text by the caller, never turned into a url
export function runbookUrl(runbook: string | null): string | null {
  if (runbook === null || !RUNBOOK_PATH.test(runbook)) return null;
  return REPOSITORY_BLOB + runbook;
}

const clockFmt = new Intl.DateTimeFormat("en", {
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
  hourCycle: "h23",
  timeZone: "UTC",
});

// the time of the last check, to the second: the page refreshes every 30 s
export function formatClockUtc(iso: string): string {
  return `${clockFmt.format(new Date(iso))} UTC`;
}
