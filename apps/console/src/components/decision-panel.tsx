// the one approve or reject ui: the choice as two radio cards, reason
// presets for a rejection, the reason the members read, keyboard a, r and
// ctrl enter, and a toast when it is done. the review queue and the
// operator project page use it for projects and for sites.

import { useMutation } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { Check, CircleAlert, RotateCcw, X } from "lucide-react";
import { type FormEvent, type KeyboardEvent, useId, useState } from "react";
import { ApiError } from "@/api/client";
import { api } from "@/api/endpoints";
import { keys } from "@/api/queries";
import { FormMessage } from "@/components/error-panel";
import { Kbd } from "@/components/panel";
import { toast } from "@/components/toast";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import * as v from "@/lib/validate";
import { queryClient } from "@/query-client";

export type Choice = "approve" | "reject";

// what is decided: a project (its enrollment) or a site of a project on a
// domain that needs a check
export type DecisionSubject =
  | { kind: "project"; id: string; name: string; allowed: readonly string[] }
  | { kind: "site"; id: string; name: string };

// presets fill the text the members read; the operator edits it afterwards
const PRESETS = [
  "Not built on Stellar.",
  "No Stellar integration shown yet.",
  "Ownership of the website not verified.",
  "Duplicate of another project.",
];

const NOUN = { project: "project", site: "site" } as const;

// approve or reject inline. a reject needs a reason, an approve takes an
// optional note (contract 5.5); both reach the members. a and r pick, ctrl
// or cmd enter sends. a project and a site use the same panel and words.
export function DecisionPanel({
  subject,
  onDecided,
}: {
  subject: DecisionSubject;
  onDecided?: (choice: Choice) => void;
}) {
  const t = subject;
  const noun = NOUN[subject.kind];
  const allowed = (["approve", "reject"] as const).filter(
    (a) => subject.kind === "site" || subject.allowed.includes(a),
  );
  const [choice, setChoice] = useState<Choice>(allowed[0] ?? "approve");
  const [reason, setReason] = useState("");
  const [fieldError, setFieldError] = useState<string | null>(null);
  const ids = useId();
  const run = useMutation({
    mutationFn: ({ act, text }: { act: Choice; text: string }): Promise<unknown> =>
      subject.kind === "site"
        ? api.operator.siteAction(t.id, act, text)
        : api.operator.tenantAction(t.id, act, text),
    onSuccess: async (_d, vars) => {
      toast.success(
        `${t.name} ${vars.act === "approve" ? "approved" : "rejected"}. The members see the decision.`,
      );
      setReason("");
      onDecided?.(vars.act);
      await queryClient.invalidateQueries({ queryKey: keys.operator });
    },
  });

  if (allowed.length === 0) return null;
  const required = choice === "reject";
  const conflict = run.error instanceof ApiError && run.error.code === "invalid_transition";

  function submit(e?: FormEvent) {
    e?.preventDefault();
    if (run.isPending) return;
    const code = v.reason(reason, required);
    setFieldError(code ? v.fieldMessage(code) : null);
    if (code) return;
    run.mutate({ act: choice, text: reason.trim() });
  }

  function pick(c: Choice) {
    if (!allowed.includes(c)) return;
    setChoice(c);
    setFieldError(null);
  }

  // the radio group pattern: one tab stop, the arrow keys move the choice
  // and the focus together
  function arrows(e: KeyboardEvent<HTMLDivElement>) {
    const step =
      e.key === "ArrowRight" || e.key === "ArrowDown"
        ? 1
        : e.key === "ArrowLeft" || e.key === "ArrowUp"
          ? -1
          : 0;
    if (step === 0 || allowed.length < 2) return;
    e.preventDefault();
    const i = allowed.indexOf(choice as (typeof allowed)[number]);
    const next = allowed[(i + step + allowed.length) % allowed.length];
    if (!next) return;
    pick(next);
    e.currentTarget.querySelector<HTMLElement>(`[data-choice="${next}"]`)?.focus();
  }

  // a and r pick, ctrl or cmd enter submits; typing in the reason is left alone
  function shortcuts(e: KeyboardEvent) {
    if ((e.ctrlKey || e.metaKey) && e.key === "Enter") {
      e.preventDefault();
      submit();
      return;
    }
    const typing = (e.target as HTMLElement).closest("textarea, input");
    if (typing || e.ctrlKey || e.metaKey || e.altKey) return;
    if (e.key === "a") pick("approve");
    if (e.key === "r") pick("reject");
  }

  return (
    <form
      aria-label={`Decision on ${t.name}`}
      onSubmit={submit}
      onKeyDown={shortcuts}
      noValidate
      className="flex flex-col gap-3"
    >
      <div
        role="radiogroup"
        aria-label="Decision"
        className="grid grid-cols-2 gap-2"
        onKeyDown={arrows}
      >
        {allowed.map((c) => {
          const on = c === choice;
          return (
            // biome-ignore lint/a11y/useSemanticElements: a radio card with a shortcut hint, like the segmented control
            <button
              key={c}
              type="button"
              role="radio"
              aria-checked={on}
              tabIndex={on ? 0 : -1}
              data-choice={c}
              aria-keyshortcuts={c === "approve" ? "A" : "R"}
              onClick={() => pick(c)}
              className={cn(
                "flex items-center justify-between gap-2 rounded-md border px-3 py-2 text-left text-body font-medium transition-colors duration-(--vs-duration-press) ease-standard focus-ring max-lg:min-h-(--vs-control-touch)",
                on
                  ? c === "approve"
                    ? "border-success bg-selected text-fg"
                    : "border-critical bg-selected text-fg"
                  : "border-line-strong text-fg-2 hover:bg-hover hover:text-fg",
              )}
            >
              <span className="flex items-center gap-2">
                {c === "approve" ? (
                  <Check aria-hidden className="size-3.5 text-success" strokeWidth={2.6} />
                ) : (
                  <X aria-hidden className="size-3.5 text-critical" strokeWidth={2.6} />
                )}
                {c === "approve" ? "Approve" : "Reject"}
              </span>
              <span aria-hidden className="max-lg:hidden">
                <Kbd>{c === "approve" ? "A" : "R"}</Kbd>
              </span>
            </button>
          );
        })}
      </div>

      {choice === "reject" ? (
        <fieldset className="flex flex-wrap gap-1">
          <legend className="sr-only">Reason presets</legend>
          {PRESETS.map((p) => {
            const on = reason.trim() === p;
            return (
              <button
                key={p}
                type="button"
                aria-pressed={on}
                onClick={() => {
                  setReason(p);
                  setFieldError(null);
                }}
                className={cn(
                  "h-7 rounded-full border px-2 text-caption transition-colors duration-(--vs-duration-press) ease-standard focus-ring max-lg:h-9",
                  on
                    ? "border-fg bg-selected text-fg"
                    : "border-line-strong text-fg-2 hover:bg-hover hover:text-fg",
                )}
              >
                {p}
              </button>
            );
          })}
        </fieldset>
      ) : null}

      <div className="flex flex-col gap-1">
        <label htmlFor={`${ids}-r`} className="text-small font-medium text-fg">
          Reason{" "}
          <span className="font-normal text-fg-3">{required ? "(required)" : "(optional)"}</span>
        </label>
        <Textarea
          id={`${ids}-r`}
          name="reason"
          rows={3}
          maxLength={1000}
          value={reason}
          aria-invalid={fieldError ? true : undefined}
          aria-describedby={`${ids}-h`}
          onChange={(e) => {
            setReason(e.target.value);
            if (fieldError) setFieldError(null);
          }}
        />
        <span id={`${ids}-h`} className="flex justify-between gap-3 text-caption">
          <span className={fieldError ? "text-critical" : "text-fg-3"}>
            {fieldError ?? "The project's members see this text."}
          </span>
          <span className="font-mono text-fg-3">{reason.length} / 1000</span>
        </span>
      </div>

      {conflict ? (
        <div
          role="alert"
          className="flex animate-fade-in flex-wrap items-start gap-2 rounded-md border border-line-strong px-3 py-2 text-small text-fg"
        >
          <CircleAlert aria-hidden className="mt-1 size-4 shrink-0 text-warning" />
          <span className="min-w-0 flex-1">
            Another operator decided first. {run.error instanceof Error ? run.error.message : null}
          </span>
          <Button
            type="button"
            variant="outline"
            size="xs"
            onClick={() => queryClient.invalidateQueries({ queryKey: keys.operator })}
          >
            <RotateCcw aria-hidden />
            Reload
          </Button>
        </div>
      ) : (
        <FormMessage error={run.error} />
      )}

      <div className="flex flex-wrap items-center justify-between gap-3">
        <span className="text-caption text-fg-3">
          Members see the reason. Recorded in the audit log.
        </span>
        <Button
          type="submit"
          variant={choice === "reject" ? "destructive" : "default"}
          pending={run.isPending}
          disabled={conflict}
          aria-keyshortcuts="Control+Enter"
          className="max-lg:h-(--vs-control-touch) max-lg:w-full"
        >
          {run.isPending
            ? choice === "reject"
              ? "Rejecting"
              : "Approving"
            : choice === "reject"
              ? `Reject ${noun}`
              : `Approve ${noun}`}
          <span aria-hidden className="font-mono text-axis font-normal opacity-70 max-lg:hidden">
            Ctrl Enter
          </span>
        </Button>
      </div>
    </form>
  );
}
