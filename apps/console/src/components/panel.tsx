import { cn } from "@veristar/brand/cn";
import type { ReactNode } from "react";

// a raised surface with an optional header. raised = border only; depth
// comes from the overlay surface, never from a shadow on a card.
export function Panel({
  title,
  description,
  actions,
  children,
  className,
  bodyClassName,
  as: As = "section",
  flush = false,
  ...rest
}: {
  title?: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  children?: ReactNode;
  className?: string;
  bodyClassName?: string;
  as?: "section" | "div" | "article";
  // the body has no padding: a table or a list runs to the panel's edge
  flush?: boolean;
} & Omit<React.HTMLAttributes<HTMLElement>, "title">) {
  return (
    <As
      className={cn("flex min-w-0 flex-col rounded-lg border border-line bg-raised", className)}
      {...rest}
    >
      {title || actions ? (
        <div className="flex flex-wrap items-start justify-between gap-x-4 gap-y-2 px-4 pt-3 pb-3 sm:px-5">
          <div className="flex min-w-0 flex-col gap-1">
            {title ? <h2 className="text-title text-fg">{title}</h2> : null}
            {description ? <p className="text-small text-fg-2">{description}</p> : null}
          </div>
          {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
        </div>
      ) : null}
      <div
        className={cn(
          flush ? "" : "px-4 pb-4 sm:px-5",
          !title && !actions && !flush && "pt-4",
          bodyClassName,
        )}
      >
        {children}
      </div>
    </As>
  );
}

// a label above a value, for definition-style facts inside a panel
export function Fact({
  label,
  children,
  className,
}: {
  label: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("flex min-w-0 flex-col gap-1", className)}>
      <dt className="text-caption text-fg-3">{label}</dt>
      <dd className="min-w-0 text-body text-fg">{children}</dd>
    </div>
  );
}

export function Kbd({ children }: { children: ReactNode }) {
  return (
    <kbd className="inline-flex h-5 min-w-5 items-center justify-center rounded-sm border border-line-strong px-1 text-axis text-fg-3">
      {children}
    </kbd>
  );
}
