import { cn } from "@veristar/brand/cn";
import { CircleAlert, Inbox, RotateCcw } from "lucide-react";
import type { ReactNode } from "react";
import { CopyButton } from "@/components/copy-button";
import { Button } from "@/components/ui/button";
import { Skeleton as Bone } from "@/components/ui/skeleton";
import { Tooltip } from "@/components/ui/tooltip";
import { errorDetails, errorMessage } from "@/lib/errors";
import { MISSING } from "@/lib/format";
import { siblingUrl } from "@/lib/hosts";

// the states every page and section shares: empty, error and loading. each
// says what happened, then what to do. the page header is never part of a
// state: it stays in place while its content loads or fails.

// the frame of every state
function StateFrame({
  tone,
  role,
  icon,
  title,
  children,
  actions,
  compact,
  className,
}: {
  tone: "neutral" | "critical";
  role: "status" | "alert";
  icon?: ReactNode;
  title: string;
  children?: ReactNode;
  actions?: ReactNode;
  compact?: boolean;
  className?: string;
}) {
  return (
    <div
      role={role}
      className={cn(
        "flex animate-fade-in flex-col items-center justify-center gap-3 rounded-lg border text-center",
        compact ? "px-4 py-6" : "px-6 py-10",
        tone === "critical" ? "border-line bg-raised" : "border-dashed border-line-strong",
        className,
      )}
    >
      {icon ? <IconTile tone={tone}>{icon}</IconTile> : null}
      <span className="flex max-w-prose flex-col gap-1">
        <span className="text-title text-fg">{title}</span>
        {children ? <span className="text-small text-fg-2">{children}</span> : null}
      </span>
      {actions ? (
        <span className="flex flex-wrap items-center justify-center gap-2">{actions}</span>
      ) : null}
    </div>
  );
}

// nothing to show yet, one anatomy everywhere: an icon tile, a title that
// says what is missing (no full stop), the text that says why or what comes
// next (sentences), and the one way forward. a dashed edge marks a section
// that is empty; a whole page that has nothing uses MessagePage
export function EmptyState({
  icon,
  title,
  children,
  action,
  compact,
  className,
}: {
  icon?: ReactNode;
  title: string;
  children?: ReactNode;
  action?: ReactNode;
  compact?: boolean;
  className?: string;
}) {
  return (
    <StateFrame
      tone="neutral"
      role="status"
      icon={icon ?? <Inbox aria-hidden strokeWidth={1.75} />}
      title={title}
      actions={action}
      compact={compact}
      className={className}
    >
      {children}
    </StateFrame>
  );
}

// a whole page that stands in for the one asked for (not found, removed,
// revoked, operators only, a crash, a link that no longer works): one solid
// card in one place, the same icon tile as every state, the page title,
// what happened and why, and the way on. a section that is only empty uses
// EmptyState instead
export function MessagePage({
  icon,
  tone = "neutral",
  title,
  children,
  actions,
  className,
}: {
  icon: ReactNode;
  tone?: "neutral" | "critical";
  title: string;
  children?: ReactNode;
  actions?: ReactNode;
  className?: string;
}) {
  return (
    <section
      data-message-page=""
      className={cn(
        "mx-auto flex w-full max-w-prose animate-rise-in flex-col gap-4 rounded-lg border border-line bg-raised px-6 py-10 sm:px-10",
        className,
      )}
    >
      <IconTile tone={tone}>{icon}</IconTile>
      <h1 className="text-heading text-fg">{title}</h1>
      {children ? (
        <div className="flex max-w-prose flex-col gap-1 text-body text-fg-2">{children}</div>
      ) : null}
      {actions ? <div className="flex flex-wrap gap-2 pt-1">{actions}</div> : null}
    </section>
  );
}

// the square that holds the icon of every state and message page
function IconTile({ tone, children }: { tone: "neutral" | "critical"; children: ReactNode }) {
  return (
    <span
      className={cn(
        "flex size-10 shrink-0 items-center justify-center rounded-lg border border-line-strong bg-raised [&_svg]:size-5",
        tone === "critical" ? "text-critical" : "text-fg-2",
      )}
    >
      {children}
    </span>
  );
}

// a request failed: the title names what failed ("The sites did not load"),
// one sentence of what happened (no request id, path or code in it), Try
// again, Copy details with what the logs need, and where to look if it
// keeps failing
export function ErrorState({
  error,
  title,
  onRetry,
  compact,
  className,
}: {
  error: unknown;
  title: string;
  onRetry?: () => void;
  compact?: boolean;
  className?: string;
}) {
  return (
    <StateFrame
      tone="critical"
      role="alert"
      icon={<CircleAlert aria-hidden strokeWidth={1.75} />}
      title={title}
      compact={compact}
      className={className}
      actions={
        <>
          {onRetry ? (
            <Button variant="outline" size="sm" onClick={onRetry}>
              <RotateCcw aria-hidden />
              Try again
            </Button>
          ) : null}
          <CopyButton text={errorDetails(error)} label="Copy details" size="sm" variant="ghost" />
        </>
      }
    >
      {errorMessage(error)}
      <StatusHint />
    </StateFrame>
  );
}

// the way on when a retry fails too: the service status page, where it
// exists
function StatusHint() {
  const status = siblingUrl(window.location.hostname, "status");
  if (!status) return null;
  return (
    <span className="mt-1 block">
      If this continues, see{" "}
      <a
        href={status}
        target="_blank"
        rel="noopener"
        className="rounded-sm text-fg underline decoration-line-strong underline-offset-4 hover:decoration-fg focus-ring"
      >
        Service status
      </a>
      .
    </span>
  );
}

// a section that is loading: skeletons in the shape of what comes, and a
// label for screen readers
export function LoadingState({
  label,
  children,
  className,
}: {
  label: string;
  children?: ReactNode;
  className?: string;
}) {
  return (
    <div role="status" aria-busy="true" className={cn("flex flex-col gap-8", className)}>
      <span className="sr-only">{label}</span>
      {children ?? <TableSkeleton rows={4} />}
    </div>
  );
}

// a row of stat cards
const STAT_COLS: Record<number, string> = {
  2: "lg:grid-cols-2",
  3: "lg:grid-cols-3",
  4: "lg:grid-cols-4",
  5: "lg:grid-cols-5",
};
function StatRowSkeleton({ count = 4 }: { count?: number }) {
  return (
    <div aria-hidden className={cn("grid grid-cols-2 gap-3", STAT_COLS[count])}>
      {Array.from({ length: count }, (_, i) => (
        <div key={i} className="flex flex-col gap-3 rounded-lg border border-line bg-raised p-4">
          <Bone className="h-3 w-1/2" />
          <Bone className="h-7 w-3/4" />
          <Bone className="h-3 w-2/5" />
        </div>
      ))}
    </div>
  );
}

// a chart card: title line and bars on the plot's height
const BAR_HEIGHTS = ["h-1/3", "h-1/2", "h-2/5", "h-3/5", "h-1/2", "h-2/3", "h-3/4", "h-1/2"];
function ChartSkeleton({ bars = 24 }: { bars?: number }) {
  return (
    <div aria-hidden className="flex flex-col gap-4 rounded-lg border border-line bg-raised p-5">
      <Bone className="h-4 w-1/4" />
      <div className="flex h-52 items-end gap-1 pl-10">
        {Array.from({ length: bars }, (_, i) => (
          <Bone
            key={i}
            className={cn("flex-1 rounded-b-none", BAR_HEIGHTS[i % BAR_HEIGHTS.length])}
          />
        ))}
      </div>
    </div>
  );
}

// a table: the header line and rows of the row height
function TableSkeleton({ rows = 6, columns = 4 }: { rows?: number; columns?: number }) {
  return (
    <div aria-hidden className="flex flex-col rounded-lg border border-line bg-raised">
      <div className="flex gap-4 border-b border-line px-4 py-3">
        {Array.from({ length: columns }, (_, i) => (
          <Bone key={i} className={cn("h-3", i === 0 ? "w-1/3" : "w-1/6")} />
        ))}
      </div>
      {Array.from({ length: rows }, (_, r) => (
        <div
          key={r}
          className="flex h-(--vs-row) items-center gap-4 border-b border-line-subtle px-4 last:border-b-0"
        >
          {Array.from({ length: columns }, (_, i) => (
            <Bone key={i} className={cn("h-3", i === 0 ? "w-1/3" : "w-1/6")} />
          ))}
        </div>
      ))}
    </div>
  );
}

// skeletons that match the final layout, used inside LoadingState
export const Skeleton = {
  StatRow: StatRowSkeleton,
  Chart: ChartSkeleton,
  Table: TableSkeleton,
};

// a value that does not exist: the en dash, with the reason in a tooltip
// and for screen readers. "0" is only ever a real zero.
export function Missing({ reason, className }: { reason: string; className?: string }) {
  return (
    <Tooltip content={reason}>
      <span className={cn("text-fg-3", className)} data-missing="">
        <span aria-hidden>{MISSING}</span>
        <span className="sr-only">{reason}</span>
      </span>
    </Tooltip>
  );
}
