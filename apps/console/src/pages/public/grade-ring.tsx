import { cn } from "@veristar/brand/cn";
import { useLayoutEffect, useRef } from "react";
import type { IntegrityRow } from "@/api/schemas";

// the integrity seal: a ring split by the share of graded site days at A, B
// and C, the range grade in its middle. the one gradient the design allows.
// the ring is set through the dom like every drawn width (strict csp), and
// the letter is always written out, so colour never carries the grade alone
export function GradeRing({
  row,
  size = "sm",
  className,
}: {
  row: Pick<IntegrityRow, "grade" | "graded_days" | "days_a" | "days_b" | "days_c"> | null;
  size?: "sm" | "lg";
  className?: string;
}) {
  const ref = useRef<HTMLSpanElement>(null);
  const graded = row && row.graded_days > 0 ? row : null;
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    if (!graded) {
      el.style.background = "var(--color-line)";
      return;
    }
    const total = graded.days_a + graded.days_b + graded.days_c || 1;
    const a = (graded.days_a / total) * 100;
    const b = a + (graded.days_b / total) * 100;
    el.style.background = `conic-gradient(var(--color-success) 0 ${a}%, var(--color-warning) ${a}% ${b}%, var(--color-critical) ${b}% 100%)`;
  }, [graded]);
  const letter = graded?.grade ?? null;
  const label = letter
    ? `Integrity grade ${letter}, ${graded?.graded_days} graded site days`
    : "Integrity grade: no graded site day";
  return (
    <span
      ref={ref}
      role="img"
      aria-label={label}
      title={label}
      data-grade={letter ?? "none"}
      className={cn(
        "relative flex shrink-0 rounded-full",
        size === "sm" ? "size-13.5" : "size-37.5",
        className,
      )}
    >
      <span
        aria-hidden
        className={cn(
          "absolute flex flex-col items-center justify-center rounded-full bg-raised text-fg",
          size === "sm" ? "inset-1.5" : "inset-3",
        )}
      >
        <span
          className={cn(
            "font-semibold",
            size === "sm" ? "text-[18px] leading-none" : "text-[56px] leading-none tracking-[-0.03em]",
          )}
        >
          {letter ?? "–"}
        </span>
        {size === "lg" ? <span className="mt-2 text-caption text-fg-2">Integrity grade</span> : null}
      </span>
    </span>
  );
}
