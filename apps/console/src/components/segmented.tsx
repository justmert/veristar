import { cn } from "@veristar/brand/cn";
import { transition } from "@veristar/brand/motion";
import { LayoutGroup, motion } from "motion/react";
import { type ReactNode, useId } from "react";

// a single choice among a few (a date range, a breakdown). arrow keys move
// the choice, as in a native radio group; the marker slides to it.
export function Segmented<T extends string>({
  label,
  value,
  options,
  onChange,
  hint,
  className,
  size = "sm",
  iconOnly = false,
}: {
  label: string;
  value: T;
  // with `iconOnly`, an option shows its icon and its label is its
  // accessible name only. `marker` sits before the label, such as the small
  // amber dot of quarantined traffic (see SegmentedDot)
  options: readonly { value: T; label: string; icon?: ReactNode; marker?: ReactNode }[];
  onChange: (v: T) => void;
  // a note after the control, e.g. the resolution the range implies
  hint?: string;
  className?: string;
  size?: "sm" | "md";
  iconOnly?: boolean;
}) {
  const group = useId();
  function key(e: React.KeyboardEvent, i: number) {
    const d =
      e.key === "ArrowRight" || e.key === "ArrowDown"
        ? 1
        : e.key === "ArrowLeft" || e.key === "ArrowUp"
          ? -1
          : 0;
    if (!d) return;
    e.preventDefault();
    const next = options[(i + d + options.length) % options.length];
    if (!next) return;
    onChange(next.value);
    const btn = (e.currentTarget.parentElement?.children[
      (i + d + options.length) % options.length
    ] ?? null) as HTMLElement | null;
    btn?.focus();
  }
  return (
    <div className={cn("flex items-center gap-2", className)}>
      <LayoutGroup id={group}>
        <div
          role="radiogroup"
          aria-label={label}
          className="inline-flex gap-0.5 rounded-md border border-line-2 bg-sunken p-0.5"
        >
          {options.map((o, i) => {
            const on = o.value === value;
            return (
              // biome-ignore lint/a11y/useSemanticElements: a native radio cannot carry the sliding marker; the arrow keys and roles follow the radio group pattern
              <button
                key={o.value}
                type="button"
                role="radio"
                aria-checked={on}
                aria-label={iconOnly ? o.label : undefined}
                title={iconOnly ? o.label : undefined}
                tabIndex={on ? 0 : -1}
                onClick={() => onChange(o.value)}
                onKeyDown={(e) => key(e, i)}
                className={cn(
                  "relative inline-flex items-center justify-center rounded-md px-2.5 [&_svg]:size-4 whitespace-nowrap transition-colors duration-(--vs-duration-reveal) ease-standard focus-ring",
                  size === "sm" ? "h-[26px] text-small" : "h-8 text-body",
                  on ? "text-fg" : "text-fg-2 hover:text-fg",
                )}
              >
                {on ? (
                  <motion.span
                    layoutId="seg-marker"
                    transition={transition.layout}
                    aria-hidden
                    className="absolute inset-0 rounded-md bg-selected"
                  />
                ) : null}
                <span className="relative flex items-center">
                  {o.marker && !iconOnly ? o.marker : null}
                  {iconOnly && o.icon ? o.icon : o.label}
                </span>
              </button>
            );
          })}
        </div>
      </LayoutGroup>
      {hint ? <span className="text-caption text-fg-3">{hint}</span> : null}
    </div>
  );
}

const DOT = {
  warning: "bg-warning",
  critical: "bg-critical",
  success: "bg-success",
} as const;

// the marker of an option: a 6 px dot in a status colour
export function SegmentedDot({ tone }: { tone: keyof typeof DOT }) {
  return <span aria-hidden className={`mr-1.5 inline-block size-1.5 rounded-full ${DOT[tone]}`} />;
}
