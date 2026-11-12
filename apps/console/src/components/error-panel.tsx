import { RotateCcw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { errorText } from "@/lib/errors";

export function ErrorPanel({
  error,
  onRetry,
  title = "This could not be loaded",
}: {
  error: unknown;
  onRetry?: () => void;
  title?: string;
}) {
  return (
    <div
      role="alert"
      className="rounded-lg border border-destructive/40 bg-destructive/5 p-4 text-sm"
    >
      <p className="font-medium text-destructive">{title}</p>
      <p className="mt-1 text-muted-foreground">{errorText(error)}</p>
      {onRetry ? (
        <Button variant="outline" size="sm" className="mt-3" onClick={onRetry}>
          <RotateCcw aria-hidden />
          Try again
        </Button>
      ) : null}
    </div>
  );
}

// inline message under a form, announced when it appears
export function FormMessage({ error }: { error: unknown }) {
  if (!error) return null;
  return (
    <p role="alert" className="text-sm text-destructive">
      {errorText(error)}
    </p>
  );
}

export function Loading({ label }: { label: string }) {
  return (
    <p role="status" className="py-8 text-sm text-muted-foreground">
      {label}
    </p>
  );
}
