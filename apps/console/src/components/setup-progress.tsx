import { cn } from "@veristar/brand/cn";
import { Check, Circle } from "lucide-react";
import { type ReactNode, useId } from "react";

// a sequential setup (a site, the API connection, project next steps). the
// steps are one bar of parts, the current step is the only one with room,
// later steps are one quiet line, done steps are quiet rows with their
// result. the same bar sits in a table cell (Sites) and a panel (Overview
// next steps) through SetupBar and SetupNext.

export type SetupStepState = "done" | "current" | "problem" | "todo";

export type SetupStep = {
  key: string;
  label: string;
  state: SetupStepState;
  // the result of a done step ("Installed", "First on Nov 18"), with its
  // link ("Show the tag")
  result?: ReactNode;
};

const PART: Record<SetupStepState, string> = {
  done: "bg-success/80",
  current: "bg-warning",
  problem: "bg-critical",
  todo: "bg-line",
};

const SAID: Record<SetupStepState, string> = {
  done: "done",
  current: "next",
  problem: "needs action",
  todo: "to do",
};

// the bar alone: one part per step, each named for assistive technology and
// on hover
export function SetupBar({
  steps,
  className,
}: {
  steps: Pick<SetupStep, "key" | "label" | "state">[];
  className?: string;
}) {
  const done = steps.filter((s) => s.state === "done").length;
  return (
    <span
      role="img"
      aria-label={`Setup ${done} of ${steps.length} done: ${steps.map((s) => `${s.label}, ${SAID[s.state]}`).join("; ")}`}
      className={cn("grid w-full gap-1", className)}
      style={{ gridTemplateColumns: `repeat(${Math.max(1, steps.length)}, minmax(0, 1fr))` }}
    >
      {steps.map((s) => (
        <i key={s.key} title={s.label} className={cn("block h-1.5 rounded-sm", PART[s.state])} />
      ))}
    </span>
  );
}

// the setup state in a row or a panel: the next step named with its place
// ("Install the tracker", "1 of 3") over the bar, or a quiet check and
// "Ready" when every step is done
export function SetupNext({
  steps,
  readyLabel = "Ready",
  status,
  className,
}: {
  steps: Pick<SetupStep, "key" | "label" | "state">[];
  readyLabel?: string;
  // a state pill (paused, in review) shown in place of the next step
  status?: ReactNode;
  className?: string;
}) {
  const next = steps.findIndex((s) => s.state !== "done");
  const ready = next === -1;
  return (
    <span className={cn("flex min-w-0 flex-col gap-2", className)}>
      {status ? (
        <span className="flex items-center">{status}</span>
      ) : ready ? (
        <span className="flex items-center gap-2 text-small text-fg-2">
          <Check aria-hidden strokeWidth={2.6} className="size-3.5 text-success" />
          {readyLabel}
        </span>
      ) : (
        <span className="flex flex-wrap items-baseline gap-x-2.5 text-small">
          <span className="font-medium text-fg">{steps[next]?.label}</span>
          <span className="text-small text-fg-3 tabular">
            {next + 1} of {steps.length}
          </span>
        </span>
      )}
      <SetupBar steps={steps} className="max-w-[260px]" />
    </span>
  );
}

// the whole setup section of a page: heading with its place and the bar,
// done steps as quiet rows, the current step large (its body in
// `children`), later steps on one quiet line, and an optional step at the
// end of that line ("stellar.toml, optional")
export function SetupProgress({
  title = "Setup",
  steps,
  children,
  currentAside,
  optional,
  className,
}: {
  title?: string;
  steps: SetupStep[];
  // the body of the current step: the tag to copy, the live wait
  children?: ReactNode;
  // at the right of the current step's title (framework tabs)
  currentAside?: ReactNode;
  // a step that is not part of the count
  optional?: { label: string; result?: ReactNode; done?: boolean };
  className?: string;
}) {
  const id = useId();
  const at = steps.findIndex((s) => s.state === "current" || s.state === "problem");
  const complete = steps.every((s) => s.state === "done");
  const done = steps.filter((s) => s.state === "done").length;
  const current = at === -1 ? null : steps[at];
  const later = at === -1 ? [] : steps.slice(at + 1);
  const doneRows = steps.filter((s) => s.state === "done");
  return (
    <section aria-labelledby={id} className={cn("flex min-w-0 flex-col gap-3", className)}>
      <div className="flex flex-wrap items-baseline gap-2">
        <h2 id={id} className="text-title leading-5 font-semibold text-fg">
          {title}
        </h2>
        <span className="text-small text-fg-3 tabular">
          {complete ? "complete" : `${done + 1} of ${steps.length}`}
        </span>
        <span className="ml-auto w-[200px] max-w-full self-center">
          <SetupBar steps={steps} />
        </span>
      </div>

      {doneRows.length && (complete || current) ? (
        <ul className="flex flex-col">
          {doneRows.map((s) => (
            <DoneRow key={s.key} label={s.label} result={s.result} />
          ))}
          {complete && optional ? (
            <DoneRow
              label={optional.label}
              result={optional.result}
              optional={!optional.done}
              className="mt-2"
            />
          ) : null}
        </ul>
      ) : null}

      {current ? (
        <div className="flex flex-col gap-4 rounded-2xl border border-line bg-raised px-6 py-5">
          <div className="flex flex-wrap items-center gap-3">
            <span
              aria-hidden
              className={cn(
                "flex size-[26px] shrink-0 items-center justify-center rounded-full text-caption font-semibold",
                current.state === "problem"
                  ? "border border-critical-line text-critical"
                  : "bg-fg text-ground",
              )}
            >
              {at + 1}
            </span>
            <h3 className="text-title text-fg">
              <span className="sr-only">Step {at + 1}: </span>
              {current.label}
            </h3>
            {currentAside ? <div className="ml-auto">{currentAside}</div> : null}
          </div>
          {children}
        </div>
      ) : null}

      {current && (later.length || optional) ? (
        <div className="flex flex-wrap items-center gap-x-4 gap-y-2 text-small text-fg-3">
          {later.length ? <span>Then</span> : null}
          {later.map((s) => (
            <span key={s.key} className="flex items-center">
              <span
                aria-hidden
                className="mr-2 inline-flex size-5 items-center justify-center rounded-full border border-line-strong text-axis leading-5"
              >
                {steps.indexOf(s) + 1}
              </span>
              {s.label}
            </span>
          ))}
          {optional ? (
            <span className="flex items-center sm:ml-auto">
              <Circle aria-hidden strokeDasharray="3 3" className="mr-2 size-4 text-fg-3" />
              {optional.label}, optional
            </span>
          ) : null}
        </div>
      ) : null}
    </section>
  );
}

function DoneRow({
  label,
  result,
  optional,
  className,
}: {
  label: string;
  result?: ReactNode;
  optional?: boolean;
  className?: string;
}) {
  return (
    <li
      className={cn(
        "grid grid-cols-[20px_minmax(0,1fr)] items-center gap-x-3 gap-y-1 border-b border-line-subtle py-2.5 text-body sm:grid-cols-[20px_220px_minmax(0,1fr)]",
        className,
      )}
    >
      {optional ? (
        <Circle aria-hidden strokeDasharray="3 3" className="size-[15px] text-fg-3" />
      ) : (
        <Check aria-hidden strokeWidth={2.6} className="size-[15px] text-success" />
      )}
      <span className={optional ? "text-fg-2" : "text-fg"}>
        {label}
        <span className="sr-only">{optional ? ", optional" : ", done"}</span>
      </span>
      {result ? (
        <span className="col-start-2 flex flex-wrap items-center gap-x-3.5 text-small text-fg-3 sm:col-start-3 [&_a]:text-small [&_a]:text-fg [&_a]:underline [&_a]:decoration-line-strong [&_a]:underline-offset-[3px] [&_button]:text-small [&_button]:text-fg [&_button]:underline [&_button]:decoration-line-strong [&_button]:underline-offset-[3px]">
          {result}
        </span>
      ) : null}
    </li>
  );
}
