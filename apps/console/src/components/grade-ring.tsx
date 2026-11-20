import { cn } from "@veristar/brand/cn";
import { useLayoutEffect, useRef } from "react";
import type { IntegrityRow } from "@/api/schemas";

// the integrity seal, one component for every page that draws it: a ring
// split by the share of graded site days at A, B and C, the range grade in
// its middle. the one gradient the design allows. the ring is set through
// the dom like every drawn width (strict csp), and the letter is always
// written out (or is the figure beside it), so colour never carries the
// grade alone.
//
// xs: 22 px, a ring with a hole and no letter (the overview figure tile,
// whose value is the letter). sm: 54 px with the letter (my projects).
// lg: 150 px with "Integrity grade" under the letter (public report).
// xl: 176 px, the same at the size of the scf review standing.
export type GradeRingSize = "xs" | "sm" | "lg" | "xl";

const BOX: Record<GradeRingSize, string> = {
  xs: "size-[22px] [mask:radial-gradient(circle,transparent_6px,#000_7px)]",
  sm: "size-13.5",
  lg: "size-37.5",
  xl: "size-44",
};
const INSET: Record<Exclude<GradeRingSize, "xs">, string> = {
  sm: "inset-1.5",
  lg: "inset-[11px] shadow-[inset_0_0_0_1px_var(--color-line-2)]",
  xl: "inset-3 shadow-[inset_0_0_0_1px_var(--color-line-2)]",
};
const LETTER: Record<Exclude<GradeRingSize, "xs">, string> = {
  sm: "text-[18px] leading-none",
  lg: "text-[56px] leading-none tracking-[-0.03em]",
  xl: "text-[64px] leading-none tracking-[-0.03em]",
};

export function GradeRing({
  row,
  size = "sm",
  className,
  ...data
}: {
  row: Pick<IntegrityRow, "grade" | "graded_days" | "days_a" | "days_b" | "days_c"> | null;
  size?: GradeRingSize;
  className?: string;
  [attr: `data-${string}`]: string | undefined;
}) {
  const ref = useRef<HTMLSpanElement>(null);
  const graded = row && row.graded_days > 0 ? row : null;
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    if (!graded) {
      el.style.background = "var(--color-line-2)";
      return;
    }
    const total = graded.days_a + graded.days_b + graded.days_c || 1;
    const a = (graded.days_a / total) * 100;
    const b = a + (graded.days_b / total) * 100;
    el.style.background = `conic-gradient(var(--color-success) 0 ${a}%, var(--color-warning) ${a}% ${b}%, var(--color-critical) ${b}% 100%)`;
  }, [graded]);
  const letter = graded?.grade ?? null;
  const label =
    letter && graded
      ? `Integrity grade ${letter}: ${graded.days_a} A days, ${graded.days_b} B days, ${graded.days_c} C days`
      : "Integrity grade: no graded site day";
  return (
    <span
      ref={ref}
      role={size === "xs" ? undefined : "img"}
      aria-hidden={size === "xs" ? true : undefined}
      aria-label={size === "xs" ? undefined : label}
      title={size === "xs" ? undefined : label}
      data-grade={letter ?? "none"}
      {...data}
      className={cn("relative flex shrink-0 rounded-full", BOX[size], className)}
    >
      {size === "xs" ? null : (
        <span
          aria-hidden
          className={cn(
            "absolute flex flex-col items-center justify-center rounded-full bg-raised text-fg",
            INSET[size],
          )}
        >
          <span className={cn("font-semibold", LETTER[size])}>{letter ?? "–"}</span>
          {size === "lg" || size === "xl" ? (
            <span className="mt-2 text-caption text-fg-2">Integrity grade</span>
          ) : null}
        </span>
      )}
    </span>
  );
}
