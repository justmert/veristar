import { cn } from "@veristar/brand/cn";
import type { ComponentProps } from "react";

// the one on and off switch of the console (canvas .sw): a 34 by 20 track,
// green when on, saved by the caller on change. name it with
// aria-labelledby or aria-label
export function Switch({
  checked,
  onCheckedChange,
  className,
  ...props
}: Omit<ComponentProps<"button">, "onClick" | "role" | "type"> & {
  checked: boolean;
  onCheckedChange: (on: boolean) => void;
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      onClick={() => onCheckedChange(!checked)}
      {...props}
      className={cn(
        "relative inline-flex h-5 w-[34px] shrink-0 items-center rounded-full transition-colors duration-(--vs-duration-press) ease-standard focus-ring disabled:cursor-not-allowed disabled:opacity-60",
        checked ? "bg-success" : "bg-line-strong",
        className,
      )}
    >
      <span
        aria-hidden
        className={cn(
          "absolute top-[3px] left-[3px] size-3.5 rounded-full transition-transform duration-(--vs-duration-press) ease-standard motion-reduce:transition-none",
          checked ? "translate-x-[14px] bg-on-accent" : "bg-fg-2",
        )}
      />
    </button>
  );
}
