// a numbered list of setup steps whose marks carry the state: a check for a
// done step, the number in ink for the one to do now, a clock while someone
// else acts, a grey number for a later step. only the current step shows its
// body, so a page offers one action at a time.

import { cn } from "@veristar/brand/cn";
import { Check, Clock } from "lucide-react";
import type { ReactNode } from "react";

export type StepState = "done" | "current" | "todo" | "waiting";

const WORD: Record<StepState, string> = {
  done: "Done",
  current: "Now",
  waiting: "Waiting",
  todo: "Later",
};

export function Steps({
  label,
  children,
  className,
}: {
  label?: string;
  children: ReactNode;
  className?: string;
}) {
  return (
    <ol aria-label={label} className={cn("flex flex-col divide-y divide-line-subtle", className)}>
      {children}
    </ol>
  );
}

export function Step({
  n,
  state,
  title,
  aside,
  children,
}: {
  n: number;
  state: StepState;
  title: ReactNode;
  // a short fact on the title line that stays in every state (a count)
  aside?: ReactNode;
  // the body: shown for the current step only
  children?: ReactNode;
}) {
  return (
    <li
      data-check={n}
      data-state={state}
      className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-3 py-3 first:pt-0 last:pb-0"
    >
      <StepMark n={n} state={state} />
      <div className="flex min-w-0 flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
        <p
          className={cn(
            "text-body",
            state === "current"
              ? "font-medium text-fg"
              : state === "done"
                ? "text-fg-2"
                : "text-fg-3",
          )}
        >
          <span className="sr-only">{WORD[state]}: </span>
          {title}
        </p>
        {aside}
      </div>
      {state === "current" && children ? (
        <div className="col-start-2 flex min-w-0 flex-col gap-3">{children}</div>
      ) : null}
    </li>
  );
}

export function StepMark({ n, state }: { n: number; state: StepState }) {
  return (
    <span
      aria-hidden
      className={cn(
        "flex size-6 shrink-0 items-center justify-center rounded-full border text-caption tabular",
        state === "done"
          ? "border-success-line text-success"
          : state === "current"
            ? "border-accent bg-accent text-on-accent"
            : "border-line text-fg-3",
      )}
    >
      {state === "done" ? (
        <Check className="size-3.5" strokeWidth={2.6} />
      ) : state === "waiting" ? (
        <Clock className="size-3.5" />
      ) : (
        n
      )}
    </span>
  );
}
