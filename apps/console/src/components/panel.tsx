import { cn } from "@veristar/brand/cn";
import { type ReactNode, useId } from "react";
import { LabeledFactItem } from "@/components/labeled-facts";
import { formatCount } from "@/lib/format";

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
  rows = false,
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
  // the body is a list of rows: each child is one row, split from the next
  // by a divider, never a bordered card of its own (no box in a box)
  rows?: boolean;
} & Omit<React.HTMLAttributes<HTMLElement>, "title">) {
  return (
    <As
      className={cn("flex min-w-0 flex-col rounded-2xl border border-line bg-raised", className)}
      {...rest}
    >
      {title || actions ? (
        <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 px-4 pt-4 pb-3 sm:px-5">
          <div className="flex min-w-0 flex-col gap-1">
            {title ? <h2 className="text-body font-semibold text-fg">{title}</h2> : null}
            {description ? <p className="text-small text-fg-2">{description}</p> : null}
          </div>
          {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
        </div>
      ) : null}
      <div
        className={cn(
          flush ? "" : rows ? "px-4 pb-1 sm:px-5" : "px-4 pb-4 sm:px-5",
          !title && !actions && !flush && !rows && "pt-4",
          rows && "flex flex-col divide-y divide-line-subtle [&>*]:py-3",
          rows && (title || actions) && "[&>*:first-child]:pt-0",
          bodyClassName,
        )}
      >
        {children}
      </div>
    </As>
  );
}

// a label above a value, for definition-style facts inside a panel: the
// same fact as LabeledFacts (labeled-facts.tsx)
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
    <LabeledFactItem label={label} size="lg" className={className}>
      {children}
    </LabeledFactItem>
  );
}

export function Kbd({ children }: { children: ReactNode }) {
  return (
    <kbd className="inline-flex h-5 min-w-5 items-center justify-center whitespace-nowrap rounded-sm align-middle border border-line-strong px-1 text-axis text-fg-3">
      {children}
    </kbd>
  );
}

// a section of a page: its heading is the section's name, with the count of
// its items as a muted number ("Members 3"), and its actions at the right.
// a page with one table gives it no heading at all.
export function Section({
  title,
  count,
  actions,
  children,
  className,
  ...data
}: {
  title: ReactNode;
  count?: number | null;
  actions?: ReactNode;
  children?: ReactNode;
  className?: string;
  [attr: `data-${string}`]: string | undefined;
}) {
  const id = useId();
  return (
    <section
      aria-labelledby={id}
      className={cn("flex min-w-0 flex-col gap-3", className)}
      {...data}
    >
      <div className="flex min-h-(--vs-control-sm) flex-wrap items-center justify-between gap-x-4 gap-y-2">
        <SectionTitle id={id} title={title} count={count} />
        {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
      </div>
      {children}
    </section>
  );
}

export function SectionTitle({
  id,
  title,
  count,
  className,
}: {
  id?: string;
  title: ReactNode;
  count?: number | null;
  className?: string;
}) {
  return (
    <h2
      id={id}
      className={cn(
        "flex items-baseline gap-2 text-[15px] leading-5 font-semibold text-fg",
        className,
      )}
    >
      {title}
      {count !== undefined && count !== null ? (
        <span className="text-small font-normal text-fg-3 tabular">{formatCount(count)}</span>
      ) : null}
    </h2>
  );
}
