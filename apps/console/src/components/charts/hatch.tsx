// the one mark of a period that is still running, on every chart: stripes of
// the bar's own colour at 135 degrees on nothing, as the canvas draws them.
// a hatch is a texture that says "not final yet", not a decoration. html
// bars use the `hatch-running` utility (index.css) with a text colour; svg
// bars fill with this pattern.

import { cn } from "@veristar/brand/cn";

// sm: 2 px stripes every 4 px (mini bars, legend swatches); lg: 3 px every
// 6 px (full charts). the same stripes, scaled to the bar
export function HatchPattern({
  id,
  className,
  size = "lg",
}: {
  id: string;
  // the fill class of the stripes, such as fill-series-1
  className: string;
  size?: "sm" | "lg";
}) {
  const p = size === "sm" ? 4 : 6;
  return (
    <pattern
      id={id}
      width={p}
      height={p}
      patternUnits="userSpaceOnUse"
      patternTransform="rotate(45)"
    >
      <rect width={p / 2} height={p} className={className} />
    </pattern>
  );
}

// the legend key of the running period
export function RunningKey({
  label = "Still running",
  className,
}: {
  label?: string;
  className?: string;
}) {
  return (
    <span
      data-legend="partial"
      className={cn("flex items-center gap-1.5 text-caption text-fg-2", className)}
    >
      <i aria-hidden className="hatch-running block size-2.5 rounded-[2px] text-fg-3" />
      {label}
    </span>
  );
}
