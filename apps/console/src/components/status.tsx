import type { SiteStatus, TenantStatus } from "@/api/schemas";

const TONE: Record<string, string> = {
  active: "bg-good/12 text-good ring-good/30",
  pending: "bg-warn/12 text-warn ring-warn/30",
  suspended: "bg-destructive/10 text-destructive ring-destructive/30",
  rejected: "bg-destructive/10 text-destructive ring-destructive/30",
  removed: "bg-muted text-muted-foreground ring-border",
};

const TEXT: Record<string, string> = {
  active: "Active",
  pending: "Pending",
  suspended: "Suspended",
  rejected: "Rejected",
  removed: "Deleted",
};

// a dot plus a word, so the status never depends on colour alone
export function StatusPill({ status }: { status: TenantStatus | SiteStatus }) {
  return (
    <span
      className={`inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset ${TONE[status] ?? ""}`}
    >
      <span aria-hidden className="size-1.5 rounded-full bg-current" />
      {TEXT[status] ?? status}
    </span>
  );
}
