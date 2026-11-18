import { cn } from "@veristar/brand/cn";
import { CircleAlert } from "lucide-react";
import { errorText } from "@/lib/errors";

// inline message under a form, announced when it appears
export function FormMessage({ error, className }: { error: unknown; className?: string }) {
  if (!error) return null;
  return (
    <p
      role="alert"
      className={cn(
        "flex animate-fade-in items-start gap-2 rounded-md border border-critical-line bg-transparent px-2 py-2 text-small text-fg",
        className,
      )}
    >
      <CircleAlert aria-hidden className="mt-1 size-3.5 shrink-0 text-critical" />
      {errorText(error)}
    </p>
  );
}
