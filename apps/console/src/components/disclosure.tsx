import { cn } from "@veristar/brand/cn";
import { ChevronRight } from "lucide-react";
import type { ReactNode } from "react";

// one way to show and hide a part of a page: a native <details> (keyboard,
// find in page and print work without script) whose summary carries a
// chevron that turns when open, never the browser's own triangle.
//   card:   a bordered block with the summary as its title row
//   inline: a quiet text toggle inside a block of text
// the summary text stays at the body size: a disclosure never outranks the
// section title above it.
export function Disclosure({
  id,
  summary,
  children,
  variant = "card",
  open,
  onToggle,
  className,
  summaryClassName,
  bodyClassName,
  ...data
}: {
  id?: string;
  summary: ReactNode;
  children: ReactNode;
  variant?: "card" | "inline";
  // controlled: the caller keeps the open state
  open?: boolean;
  onToggle?: (open: boolean) => void;
  className?: string;
  summaryClassName?: string;
  bodyClassName?: string;
  [attr: `data-${string}`]: string | number | undefined;
}) {
  return (
    <details
      id={id}
      open={open}
      onToggle={onToggle ? (e) => onToggle(e.currentTarget.open) : undefined}
      className={cn(
        "group/disclosure",
        variant === "card" && "rounded-lg border border-line bg-raised",
        className,
      )}
      {...data}
    >
      <summary
        className={cn(
          "flex cursor-pointer list-none items-center gap-2 transition-colors duration-(--vs-duration-press) ease-standard focus-ring [&::-webkit-details-marker]:hidden",
          variant === "card" &&
            "min-h-10 rounded-lg px-4 py-2 text-body font-medium text-fg hover:bg-hover max-lg:min-h-(--vs-control-touch)",
          variant === "inline" &&
            "w-fit rounded-sm text-small text-fg-2 hover:text-fg max-lg:min-h-(--vs-control-touch)",
          summaryClassName,
        )}
      >
        <ChevronRight
          aria-hidden
          className="size-4 shrink-0 text-fg-3 transition-transform duration-(--vs-duration-press) ease-standard group-open/disclosure:rotate-90"
        />
        {summary}
      </summary>
      <div
        className={cn(
          variant === "card" && "border-t border-line p-4",
          variant === "inline" && "pt-2",
          bodyClassName,
        )}
      >
        {children}
      </div>
    </details>
  );
}
