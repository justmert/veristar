import { cn } from "@veristar/brand/cn";
import { CircleAlert } from "lucide-react";
import type { ReactNode } from "react";
import { ErrorState, StateFrame } from "@/components/states";
import { Skeleton } from "@/components/ui/skeleton";
import { errorText } from "@/lib/errors";

// the older state blocks, drawn by the shared states (states.tsx) so every
// page looks the same while it moves to EmptyState, ErrorState and
// LoadingState
export function StateBlock({
  kind,
  icon,
  title,
  children,
  action,
  className,
}: {
  kind: "empty" | "error";
  icon?: ReactNode;
  title: string;
  children?: ReactNode;
  action?: ReactNode;
  className?: string;
}) {
  return (
    <StateFrame
      tone={kind === "error" ? "critical" : "neutral"}
      role={kind === "error" ? "alert" : "status"}
      icon={kind === "error" && !icon ? <CircleAlert aria-hidden strokeWidth={1.75} /> : icon}
      title={title}
      actions={action}
      className={className}
    >
      {children}
    </StateFrame>
  );
}

export function ErrorPanel({
  error,
  onRetry,
  title,
  className,
}: {
  error: unknown;
  onRetry?: () => void;
  title?: string;
  className?: string;
}) {
  return <ErrorState error={error} onRetry={onRetry} title={title} className={className} />;
}

// inline message under a form, announced when it appears
export function FormMessage({ error, className }: { error: unknown; className?: string }) {
  if (!error) return null;
  return (
    <p
      role="alert"
      className={cn(
        "flex animate-fade-in items-start gap-2 rounded-md border border-critical-line bg-critical-soft px-2 py-2 text-small text-fg",
        className,
      )}
    >
      <CircleAlert aria-hidden className="mt-1 size-3.5 shrink-0 text-critical" />
      {errorText(error)}
    </p>
  );
}

// a loading state in the shape of a page section: a heading line, a body
// line and rows. the label is read by screen readers.
export function Loading({
  label,
  rows = 3,
  className,
}: {
  label: string;
  rows?: number;
  className?: string;
}) {
  return (
    <div role="status" aria-busy="true" className={cn("flex flex-col gap-3 py-2", className)}>
      <span className="sr-only">{label}</span>
      <Skeleton className="h-3 w-2/5" />
      <Skeleton className="h-3 w-3/5" />
      {Array.from({ length: rows }, (_, i) => (
        <Skeleton key={i} className="h-9 w-full" />
      ))}
    </div>
  );
}
