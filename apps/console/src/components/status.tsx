import { cn } from "@veristar/brand/cn";
import type { SiteStatus, TenantStatus } from "@/api/schemas";

export type Tone = "success" | "warning" | "critical" | "info" | "accent";

const DOT: Record<Tone, string> = {
  success: "bg-success",
  warning: "bg-warning",
  critical: "bg-critical",
  info: "bg-fg-3",
  accent: "bg-accent",
};

const TEXT: Record<Tone, string> = {
  success: "text-success",
  warning: "text-warning",
  critical: "text-critical",
  info: "text-fg-2",
  accent: "text-accent",
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
      className={cn("relative inline-flex size-1.5 shrink-0 rounded-full", DOT[tone], className)}
    >
      {live ? (
        <span className={cn("absolute inset-0 animate-live rounded-full", DOT[tone], TEXT[tone])} />
      ) : null}
    </span>
  );
}

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
      className={cn(
        "inline-flex h-6 shrink-0 items-center gap-1.5 whitespace-nowrap rounded-full border border-line-strong px-2 text-caption font-medium text-fg",
        className,
      )}
    >
      <StatusDot tone={tone} live={live} />
      {children}
    </span>
  );
}

const STATUS: Record<string, { tone: Tone; text: string }> = {
  active: { tone: "success", text: "Active" },
  pending: { tone: "warning", text: "Pending" },
  suspended: { tone: "info", text: "Suspended" },
  rejected: { tone: "critical", text: "Rejected" },
  removed: { tone: "info", text: "Deleted" },
};

export function statusTone(status: string): Tone {
  return STATUS[status]?.tone ?? "info";
}

export function StatusPill({
  status,
  className,
}: {
  status: TenantStatus | SiteStatus;
  className?: string;
}) {
  const s = STATUS[status] ?? { tone: "info" as Tone, text: status };
  return (
    <Pill tone={s.tone} className={className}>
      {s.text}
    </Pill>
  );
}
