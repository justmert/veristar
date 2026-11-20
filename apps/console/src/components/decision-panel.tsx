// the one approve or reject ui: a bar that closes a case. it holds the way
// to the project, Reject (r) and Approve (a). either button swaps the bar
// for the reason: presets and the text the members read for a rejection, an
// optional note for an approval, sent with ctrl enter and dropped with
// escape. nothing is sent by a single key. the review queue and the
// operator project page use it for projects and for sites.

import { useMutation } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { CircleAlert, RotateCcw, X } from "lucide-react";
import {
  type FormEvent,
  type KeyboardEvent,
  type ReactNode,
  useEffect,
  useId,
  useState,
} from "react";
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

const MAX = 1000;

export function DecisionPanel({
  subject,
  onDecided,
  lead,
  globalKeys = false,
  className,
}: {
  subject: DecisionSubject;
  onDecided?: (choice: Choice) => void;
  // at the left of the bar: the way to the project
  lead?: ReactNode;
  // r and a open the reason from anywhere on the page (the review queue,
  // where the case is the page); otherwise only inside the bar
  globalKeys?: boolean;
  className?: string;
}) {
  const t = subject;
  const allowed = (["approve", "reject"] as const).filter(
    (a) => subject.kind === "site" || subject.allowed.includes(a),
  );
  const [choice, setChoice] = useState<Choice | null>(null);
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
      setChoice(null);
      onDecided?.(vars.act);
      await queryClient.invalidateQueries({ queryKey: keys.operator });
    },
  });

  function pick(c: Choice) {
    if (!allowed.includes(c)) return;
    setChoice(c);
    setFieldError(null);
    run.reset();
  }

  // r and a anywhere on the page while no field has focus
  useEffect(() => {
    if (!globalKeys || choice !== null) return;
    function onKey(e: globalThis.KeyboardEvent) {
      if (e.ctrlKey || e.metaKey || e.altKey) return;
      const el = e.target as HTMLElement | null;
      if (el?.closest("input, textarea, select, [contenteditable=true], dialog")) return;
      if (e.key === "a" || e.key === "r") {
        e.preventDefault();
        pick(e.key === "a" ? "approve" : "reject");
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });

  if (allowed.length === 0) return null;
  const noun = subject.kind === "site" ? "site" : t.name;
  const conflict = run.error instanceof ApiError && run.error.code === "invalid_transition";

  function submit(e?: FormEvent) {
    e?.preventDefault();
    if (run.isPending || choice === null) return;
    const code = v.reason(reason, choice === "reject");
    setFieldError(code ? v.fieldMessage(code) : null);
    if (code) return;
    run.mutate({ act: choice, text: reason.trim() });
  }

  function cancel() {
    setChoice(null);
    setFieldError(null);
    run.reset();
  }

  // inside the bar: r and a pick, ctrl or cmd enter sends, escape drops
  function shortcuts(e: KeyboardEvent) {
    if ((e.ctrlKey || e.metaKey) && e.key === "Enter") {
      e.preventDefault();
      submit();
      return;
    }
    if (e.key === "Escape" && choice !== null) {
      e.preventDefault();
      cancel();
      return;
    }
    const typing = (e.target as HTMLElement).closest("textarea, input");
    if (typing || e.ctrlKey || e.metaKey || e.altKey || choice !== null || globalKeys) return;
    if (e.key === "a") pick("approve");
    if (e.key === "r") pick("reject");
  }

  return (
    <form
      aria-label={`Decision on ${t.name}`}
      onSubmit={submit}
      onKeyDown={shortcuts}
      noValidate
      data-decision={choice ?? ""}
      className={cn("flex flex-col gap-3", className)}
    >
      {choice === null ? (
        <div className="flex flex-wrap items-center gap-3">
          <div className="min-w-0 flex-1 text-small">{lead}</div>
          {allowed.includes("reject") ? (
            <Button
              type="button"
              variant="outline"
              aria-keyshortcuts="R"
              onClick={() => pick("reject")}
              className="max-lg:h-(--vs-control-touch)"
            >
              <X aria-hidden className="text-critical" />
              {subject.kind === "site" ? "Reject site" : "Reject"}
              <span aria-hidden className="max-lg:hidden">
                <Kbd>R</Kbd>
              </span>
            </Button>
          ) : null}
          {allowed.includes("approve") ? (
            <Button
              type="button"
              aria-keyshortcuts="A"
              onClick={() => pick("approve")}
              className="max-lg:h-(--vs-control-touch) max-sm:flex-1"
            >
              {subject.kind === "site" ? "Approve site" : `Approve ${t.name}`}
              <span
                aria-hidden
                className="max-lg:hidden [&>kbd]:border-current [&>kbd]:text-current"
              >
                <Kbd>A</Kbd>
              </span>
            </Button>
          ) : null}
        </div>
      ) : (
        <>
          <div className="flex items-baseline justify-between gap-3">
            <label htmlFor={`${ids}-r`} className="text-body font-semibold text-fg">
              {choice === "reject" ? "Why reject" : "Note for the members"}
              {choice === "approve" ? (
                <span className="font-normal text-fg-3"> (optional)</span>
              ) : null}
            </label>
            <span className="shrink-0 font-mono text-caption text-fg-3 tabular">
              {reason.length} / {MAX}
            </span>
          </div>
          {choice === "reject" ? (
            <fieldset className="flex flex-wrap gap-1.5">
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
                      "min-h-7 rounded-full border px-2.5 py-1 text-left text-small transition-colors duration-(--vs-duration-press) ease-standard focus-ring max-lg:min-h-9",
                      on
                        ? "border-line-focus bg-selected text-fg"
                        : "border-line-2 text-fg-2 hover:bg-hover hover:text-fg",
                    )}
                  >
                    {p}
                  </button>
                );
              })}
            </fieldset>
          ) : null}
          <Textarea
            id={`${ids}-r`}
            name="reason"
            rows={2}
            maxLength={MAX}
            value={reason}
            autoFocus
            aria-invalid={fieldError ? true : undefined}
            aria-describedby={`${ids}-h`}
            onChange={(e) => {
              setReason(e.target.value);
              if (fieldError) setFieldError(null);
            }}
          />
          {conflict ? (
            <div
              role="alert"
              className="flex animate-fade-in flex-wrap items-start gap-2 rounded-md border border-line-strong px-3 py-2 text-small text-fg"
            >
              <CircleAlert aria-hidden className="mt-1 size-4 shrink-0 text-warning" />
              <span className="min-w-0 flex-1">
                Another operator decided first.{" "}
                {run.error instanceof Error ? run.error.message : null}
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
          <div className="flex flex-wrap items-center gap-3">
            <span
              id={`${ids}-h`}
              className={cn(
                "min-w-0 flex-1 text-small",
                fieldError ? "text-critical" : "text-fg-3",
              )}
            >
              {fieldError ??
                (choice === "reject"
                  ? "The project's members see this reason"
                  : "The project's members see this note")}
            </span>
            <Button
              type="button"
              variant="ghost"
              onClick={cancel}
              aria-keyshortcuts="Escape"
              className="max-lg:h-(--vs-control-touch)"
            >
              Cancel
              <span aria-hidden className="max-lg:hidden">
                <Kbd>Esc</Kbd>
              </span>
            </Button>
            <Button
              type="submit"
              variant={choice === "reject" ? "destructive-outline" : "default"}
              pending={run.isPending}
              disabled={conflict}
              aria-keyshortcuts="Control+Enter"
              className="max-lg:h-(--vs-control-touch) max-sm:w-full"
            >
              {run.isPending
                ? choice === "reject"
                  ? "Rejecting"
                  : "Approving"
                : choice === "reject"
                  ? `Reject ${noun}`
                  : `Approve ${noun}`}
              {run.isPending ? null : (
                <span
                  aria-hidden
                  className="max-lg:hidden [&>kbd]:border-current [&>kbd]:text-current"
                >
                  <Kbd>Ctrl Enter</Kbd>
                </span>
              )}
            </Button>
          </div>
        </>
      )}
    </form>
  );
}
