import { cn } from "@veristar/brand/cn";
import { CircleAlert, RotateCcw } from "lucide-react";
import type { ReactNode } from "react";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { errorText } from "@/lib/errors";

// the one state component (system v2, R10): empty, no match, loading and
// error look the same everywhere and always say what to do next
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
    <div
      role={kind === "error" ? "alert" : "status"}
      className={cn(
        "flex animate-fade-in flex-col items-center justify-center gap-3 rounded-lg border px-6 py-10 text-center",
        kind === "empty" ? "border-dashed border-line-strong" : "border-line bg-raised",
        className,
      )}
    >
      {icon ? (
        <span
          className={cn(
            "flex size-10 items-center justify-center rounded-lg border border-line-strong [&_svg]:size-5",
            kind === "error" ? "text-critical" : "text-fg-2",
          )}
        >
          {icon}
        </span>
      ) : null}
      <span className="flex max-w-sm flex-col gap-1">
        <span className="text-title text-fg">{title}</span>
        {children ? <span className="text-small text-fg-2">{children}</span> : null}
      </span>
      {action}
    </div>
  );
}

export function ErrorPanel({
  error,
  onRetry,
  title = "This could not be loaded",
  className,
}: {
  error: unknown;
  onRetry?: () => void;
  title?: string;
  className?: string;
}) {
  return (
    <StateBlock
      kind="error"
      icon={<CircleAlert aria-hidden strokeWidth={1.75} />}
      title={title}
      className={className}
      action={
        onRetry ? (
          <Button variant="outline" onClick={onRetry}>
            <RotateCcw aria-hidden />
            Try again
          </Button>
        ) : null
      }
    >
      {errorText(error)}
    </StateBlock>
  );
}

// inline message under a form, announced when it appears
export function FormMessage({ error, className }: { error: unknown; className?: string }) {
  if (!error) return null;
  return (
    <p
      role="alert"
      className={cn(
        "flex animate-fade-in items-start gap-2 rounded-md border border-critical-line bg-critical-soft px-2.5 py-2 text-small text-fg",
        className,
      )}
    >
      <CircleAlert aria-hidden className="mt-0.5 size-3.5 shrink-0 text-critical" />
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
        // biome-ignore lint/suspicious/noArrayIndexKey: placeholders have no identity
        <Skeleton key={i} className="h-9 w-full" />
      ))}
    </div>
  );
}
