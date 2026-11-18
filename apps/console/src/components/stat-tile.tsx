import type { ReactNode } from "react";
import { StatCard, type StatChange } from "@/components/stat-card";

export { Sparkline } from "@/components/stat-card";
export type Trend = StatChange;

// the older tile api of the web, api and overview pages, drawn by the shared
// StatCard so every number card looks and behaves the same
export function StatTile({
  label,
  hint,
  value,
  numeric,
  format,
  trend,
  spark,
  state = "data",
  emptyText,
  pressed,
  onPress,
  className,
  footer,
}: {
  label: string;
  hint?: string;
  value: string;
  numeric?: number;
  format?: (n: number) => string;
  trend?: Trend | null;
  spark?: ReactNode;
  state?: "data" | "loading" | "empty" | "error";
  emptyText?: string;
  pressed?: boolean;
  onPress?: () => void;
  className?: string;
  footer?: ReactNode;
}) {
  return (
    <StatCard
      label={label}
      definition={hint}
      value={state === "empty" ? null : numeric !== undefined && format ? numeric : value}
      format={format}
      missingReason={emptyText}
      change={trend}
      spark={spark}
      selected={pressed}
      onSelect={onPress}
      state={state === "empty" ? "data" : state}
      footer={footer}
      className={className}
    />
  );
}
