// the one filter chip of the console: the site chips of the overview, web
// and privacy pages and the service chips of the api page. a pressed chip is
// a filter that is on; in a required single choice (ChipRadioGroup) the
// chips are radios, moved with the arrow keys as in a native radio group.

import { cn } from "@veristar/brand/cn";
import { Plus } from "lucide-react";
import type { KeyboardEvent, ReactNode } from "react";

export const CHIP =
  "inline-flex h-8 max-w-full items-center gap-2 rounded-full border px-3 text-small transition-colors duration-(--vs-duration-press) focus-ring max-lg:h-(--vs-control-touch)";

export function chipTone(on: boolean): string {
  return on
    ? "border-line-focus bg-selected text-fg"
    : "border-line bg-raised text-fg-2 hover:text-fg";
}

export function FilterChip({
  on,
  onClick,
  children,
  title,
  className,
  ...data
}: {
  on: boolean;
  onClick: () => void;
  children: ReactNode;
  title?: string;
  className?: string;
  [attr: `data-${string}`]: string | undefined;
}) {
  return (
    <button
      type="button"
      aria-pressed={on}
      onClick={onClick}
      title={title}
      {...data}
      className={cn(CHIP, chipTone(on), className)}
    >
      {children}
    </button>
  );
}

// the last chip of a row that adds an item, such as "Add site"
export function AddChip({ onClick, children }: { onClick: () => void; children: ReactNode }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(CHIP, "border-dashed border-line text-fg-2 hover:text-fg")}
    >
      <Plus aria-hidden className="size-3.5" />
      {children}
    </button>
  );
}

// a required single choice drawn as chips: one is always chosen (or none
// yet, before the first choice), there is no "all" chip
export function ChipRadioGroup<T extends string>({
  label,
  labelledBy,
  value,
  options,
  onChange,
  className,
}: {
  // the accessible name, or the id of the visible label
  label?: string;
  labelledBy?: string;
  value: T | null;
  options: readonly { value: T; label: ReactNode; title?: string }[];
  onChange: (v: T) => void;
  className?: string;
}) {
  // the chosen chip takes the tab stop, or the first while none is chosen
  const stop = options.some((o) => o.value === value) ? value : (options[0]?.value ?? null);
  function key(e: KeyboardEvent<HTMLButtonElement>, i: number) {
    const d =
      e.key === "ArrowRight" || e.key === "ArrowDown"
        ? 1
        : e.key === "ArrowLeft" || e.key === "ArrowUp"
          ? -1
          : 0;
    if (!d) return;
    e.preventDefault();
    const j = (i + d + options.length) % options.length;
    const next = options[j];
    if (!next) return;
    onChange(next.value);
    (e.currentTarget.parentElement?.children[j] as HTMLElement | undefined)?.focus();
  }
  return (
    <div
      role="radiogroup"
      aria-label={label}
      aria-labelledby={labelledBy}
      className={cn("flex min-w-0 flex-wrap items-center gap-1.5", className)}
    >
      {options.map((o, i) => {
        const on = o.value === value;
        return (
          // biome-ignore lint/a11y/useSemanticElements: a chip that wraps, keyed like a native radio group
          <button
            key={o.value}
            type="button"
            role="radio"
            aria-checked={on}
            tabIndex={o.value === stop ? 0 : -1}
            title={o.title}
            onClick={() => onChange(o.value)}
            onKeyDown={(e) => key(e, i)}
            className={cn(CHIP, chipTone(on))}
          >
            <span className="truncate">{o.label}</span>
          </button>
        );
      })}
    </div>
  );
}
