import { cn } from "@veristar/brand/cn";
import type { IntegrityGrade, SiteStatus, TenantStatus } from "@/api/schemas";

export type Tone = "success" | "warning" | "critical" | "info" | "accent";

const DOT: Record<Tone, string> = {
  success: "bg-success",
  warning: "bg-warning",
  critical: "bg-critical",
  info: "bg-fg-3",
  accent: "bg-accent-text",
};

const TEXT: Record<Tone, string> = {
  success: "text-success",
  warning: "text-warning",
  critical: "text-critical",
  info: "text-fg-2",
  accent: "text-accent-text",
};

export function toneText(t: Tone) {
  return TEXT[t];
}

// a status is a dot plus a word, never colour alone. `live` breathes: the
// thing is listening (a first event, a verification check, alerts refresh)
export function StatusDot({
  tone,
  live,
  className,
}: {
  tone: Tone;
  live?: boolean;
  className?: string;
}) {
  return (
    <span
      aria-hidden
      className={cn("relative inline-flex size-2 shrink-0 rounded-full", DOT[tone], className)}
    >
      {live ? (
        <span className={cn("absolute inset-0 animate-live rounded-full", DOT[tone], TEXT[tone])} />
      ) : null}
    </span>
  );
}

// the fill and edge of a pill per tone: a status tints its pill, a neutral
// one stays an outline. the word is always fg, the dot carries the tone
const PILL: Record<Tone, string> = {
  success: "border-success-line bg-success-soft",
  warning: "border-warning-line bg-warning-soft",
  critical: "border-critical-line bg-critical-soft",
  info: "border-line-strong bg-transparent",
  accent: "border-line-strong bg-transparent",
};

// a pill never truncates: its word is the meaning. a narrow cell wraps the
// pill to its own line instead
export function Pill({
  tone,
  live,
  children,
  className,
}: {
  tone: Tone;
  live?: boolean;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <span
      data-tone={tone}
      className={cn(
        "inline-flex h-6 shrink-0 items-center gap-1 whitespace-nowrap rounded-full border px-2 text-caption font-medium text-fg",
        PILL[tone],
        className,
      )}
    >
      <StatusDot tone={tone} live={live} />
      {children}
    </span>
  );
}

// a count on a nav item or a button (projects waiting, alerts firing): one
// shape for every count, tinted by what it means. the words for a screen
// reader go in `label`
export function CountBadge({
  tone,
  label,
  children,
  className,
}: {
  tone: "neutral" | "warning" | "critical";
  label: string;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <span
      data-tone={tone}
      className={cn(
        "inline-flex h-5 min-w-5 shrink-0 items-center justify-center gap-1 rounded-full border px-1 text-axis font-medium tabular",
        tone === "critical"
          ? "border-critical-line bg-critical-soft text-critical"
          : tone === "warning"
            ? "border-warning-line bg-warning-soft text-warning"
            : "border-line-strong bg-selected text-fg",
        className,
      )}
    >
      <span aria-hidden className="inline-flex items-center [&_svg]:size-3">
        {children}
      </span>
      <span className="sr-only">{label}</span>
    </span>
  );
}

// the status vocabulary of the console: one word and tone per status, used
// by StatusBadge (status-badge.tsx) and wherever a status is said in text.
// the tones: success when it works (active, verified, set up), warning when
// someone must act (suspended, a domain to verify), critical when something
// is refused or failing (not approved, events refused, a failed check),
// neutral (info) for a normal wait or an end (in review for members,
// removed, expired). operators see a project in review as needing action,
// its members see it as waiting.

export type StatusEntry = { tone: Tone; word: string };

const PROJECT: Record<TenantStatus, StatusEntry> = {
  active: { tone: "success", word: "Active" },
  pending: { tone: "info", word: "In review" },
  rejected: { tone: "critical", word: "Not approved" },
  suspended: { tone: "warning", word: "Suspended" },
  removed: { tone: "info", word: "Removed" },
};

const SITE: Record<SiteStatus, StatusEntry> = {
  active: { tone: "success", word: "Active" },
  pending: { tone: "info", word: "In review" },
  suspended: { tone: "warning", word: "Suspended" },
  removed: { tone: "info", word: "Removed" },
};

const KEY: Record<"active" | "expired" | "revoked", StatusEntry> = {
  active: { tone: "success", word: "Active" },
  expired: { tone: "info", word: "Expired" },
  revoked: { tone: "info", word: "Revoked" },
};

const ALERT: Record<"page" | "ticket" | "suppressed", StatusEntry> = {
  page: { tone: "critical", word: "Act now" },
  ticket: { tone: "warning", word: "Act today" },
  suppressed: { tone: "info", word: "Silenced" },
};

// how far a site is set up: one word per site on the sites list and the
// site's setup page. waiting for a first event is a normal state; refused
// events and a failed domain check need action.
export type SetupStatus = "checking" | "waiting" | "refused" | "unverified" | "failed" | "done";

const SETUP: Record<SetupStatus, StatusEntry> = {
  checking: { tone: "info", word: "Checking" },
  waiting: { tone: "info", word: "No events yet" },
  refused: { tone: "critical", word: "Events refused" },
  unverified: { tone: "warning", word: "Domain not verified" },
  failed: { tone: "critical", word: "Domain check failed" },
  done: { tone: "success", word: "Set up" },
};

const GRADE: Record<IntegrityGrade, StatusEntry> = {
  A: { tone: "success", word: "Grade A" },
  B: { tone: "warning", word: "Grade B" },
  C: { tone: "critical", word: "Grade C" },
};

export type BadgeSubject =
  | { kind: "project"; status: TenantStatus; operator?: boolean }
  // a site follows its project: under a project that is not active the
  // site says the project's status, never a green "Active"
  | { kind: "site"; status: SiteStatus; operator?: boolean; project?: TenantStatus }
  | { kind: "key"; status: "active" | "expired" | "revoked" }
  | { kind: "alert"; status: "page" | "ticket" | "suppressed" }
  | { kind: "setup"; status: SetupStatus }
  | { kind: "grade"; grade: IntegrityGrade | null };

// the operator acts on what waits for review; its members only wait
function waiting(e: StatusEntry, status: string, operator?: boolean): StatusEntry {
  return operator && status === "pending" ? { tone: "warning", word: e.word } : e;
}

// the word and tone of a status, for places that show it without a badge
export function statusEntry(s: BadgeSubject): StatusEntry {
  switch (s.kind) {
    case "project":
      return waiting(PROJECT[s.status], s.status, s.operator);
    case "site":
      if (s.project && s.project !== "active")
        return waiting(PROJECT[s.project], s.project, s.operator);
      return waiting(SITE[s.status], s.status, s.operator);
    case "key":
      return KEY[s.status];
    case "alert":
      return ALERT[s.status];
    case "setup":
      return SETUP[s.status];
    case "grade":
      return s.grade === null ? { tone: "info", word: "Not graded" } : GRADE[s.grade];
  }
}

// the older names, said with the same words
export function statusText(status: TenantStatus | SiteStatus | string): string {
  return statusEntry({ kind: "project", status: status as TenantStatus }).word ?? status;
}

export function statusTone(status: TenantStatus | SiteStatus | string): Tone {
  return statusEntry({ kind: "project", status: status as TenantStatus })?.tone ?? "info";
}

export function StatusPill({
  status,
  className,
}: {
  status: TenantStatus | SiteStatus;
  className?: string;
}) {
  const e = statusEntry({ kind: "project", status });
  return (
    <Pill tone={e.tone} className={className}>
      {e.word}
    </Pill>
  );
}
