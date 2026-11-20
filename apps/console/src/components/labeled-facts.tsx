import { cn } from "@veristar/brand/cn";
import type { ReactNode } from "react";

export type LabeledFact = { label: ReactNode; value: ReactNode; key?: string };

// facts of one object as a row: each label above its value, never a
// "label: value" string and never joined with a separator (SCF program,
// round, category, cohort; the facts of a project or a key)
export function LabeledFacts({
  items,
  className,
  size = "md",
}: {
  items: LabeledFact[];
  className?: string;
  // md: 11.5 over 13.5 px as in the SCF standing; lg: 12 over 14 px
  size?: "md" | "lg";
}) {
  return (
    <dl className={cn("flex flex-wrap gap-x-5 gap-y-3", className)}>
      {items.map((f, i) => (
        <LabeledFactItem
          key={f.key ?? (typeof f.label === "string" ? f.label : i)}
          label={f.label}
          size={size}
        >
          {f.value}
        </LabeledFactItem>
      ))}
    </dl>
  );
}

// one fact: the label above the value. inside a <dl>
export function LabeledFactItem({
  label,
  children,
  size = "md",
  className,
}: {
  label: ReactNode;
  children: ReactNode;
  size?: "md" | "lg";
  className?: string;
}) {
  return (
    <div className={cn("flex min-w-0 flex-col gap-0.5", className)}>
      <dt className={cn("text-fg-3", size === "md" ? "text-axis leading-4" : "text-caption")}>
        {label}
      </dt>
      <dd
        className={cn(
          "min-w-0 break-words text-fg",
          size === "md" ? "text-small leading-5" : "text-body",
        )}
      >
        {children}
      </dd>
    </div>
  );
}
