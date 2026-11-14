// the review decision (system v2 R5, C-Decision): the evidence of a project
// as a checklist of signals next to the approve or reject choice, with the
// reason the project's members read. shared by the review workspace and the
// project page.

import { useMutation } from "@tanstack/react-query";
import { cn } from "cn";
import { Check, CircleAlert, Clock, Minus, RotateCcw, X } from "lucide-react";
import { type FormEvent, type KeyboardEvent, type ReactNode, useId, useState } from "react";
import { ApiError } from "@/api/client";
import { api } from "@/api/endpoints";
import { keys } from "@/api/queries";
import type { OperatorTenantDetail } from "@/api/schemas";
import { FormMessage } from "@/components/error-panel";
import { Kbd } from "@/components/panel";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { formatCount, formatDate } from "@/lib/format";
import * as v from "@/lib/validate";
import { queryClient } from "@/query-client";

export async function refreshOperator() {
  await queryClient.invalidateQueries({ queryKey: keys.operator });
}

// "today", "5 h", "3 d": how long something has waited, for the queue
export function ageText(iso: string, now: number): string {
  const h = Math.floor((now - new Date(iso).getTime()) / 3_600_000);
  if (h < 1) return "now";
  if (h < 24) return `${h} h`;
  return `${Math.floor(h / 24)} d`;
}

export function waitingText(iso: string, now: number): string {
  const d = Math.floor((now - new Date(iso).getTime()) / 86_400_000);
  if (d < 1) return "Waiting since today";
  return d === 1 ? "Waiting 1 day" : `Waiting ${d} days`;
}

// ---- evidence

export type Signal = "ok" | "risk" | "absent";

const SIGNAL_ICON: Record<Signal, ReactNode> = {
  ok: <Check aria-hidden className="size-3.5 text-success" strokeWidth={2.6} />,
  risk: <CircleAlert aria-hidden className="size-3.5 text-warning" strokeWidth={2.4} />,
  absent: <Minus aria-hidden className="size-3.5 text-fg-3" strokeWidth={2.4} />,
};

const SIGNAL_WORD: Record<Signal, string> = { ok: "proven", risk: "risk", absent: "absent" };

export type EvidenceItem = { key: string; label: string; value: ReactNode; signal: Signal };

// the facts the decision rests on, each with its signal: green for proven,
// amber for a risk, grey for absent. the word is read with the icon, so the
// signal never depends on colour.
export function evidenceOf(detail: OperatorTenantDetail): EvidenceItem[] {
  const t = detail.tenant;
  const sites = detail.sites;
  const verified = sites.filter((s) => s.verification === "verified");
  const toml = sites.filter((s) => s.stellar_toml?.status === "found");
  const prior = sites.filter((s) => s.domain_previously_removed);
  const ids = detail.stellar_ids;
  const invalid = ids.filter((s) => !s.valid);
  const events = Object.values(detail.ingest.last_30d).reduce((a, b) => a + b, 0);
  const accepted = detail.ingest.last_30d.accepted ?? 0;
  const items: EvidenceItem[] = [
    {
      key: "domain",
      label: "Domain",
      signal: verified.length > 0 ? "ok" : "absent",
      value:
        sites.length === 0
          ? "No site yet"
          : `${verified.length} of ${sites.length} ${sites.length === 1 ? "site" : "sites"} verified`,
    },
    {
      key: "toml",
      label: "stellar.toml",
      signal: toml.length > 0 ? "ok" : "absent",
      value:
        toml.length > 0
          ? `Found on ${toml.map((s) => s.domain).join(", ")}`
          : sites.length === 0
            ? "No site to check"
            : "Not found",
    },
    {
      key: "ids",
      label: "Account and contract ids",
      signal: ids.length === 0 ? "absent" : invalid.length > 0 ? "risk" : "ok",
      value:
        ids.length === 0
          ? "None given"
          : invalid.length > 0
            ? `${invalid.length} of ${ids.length} not a valid id`
            : `${ids.length} valid`,
    },
    {
      key: "scf",
      label: "SCF round",
      signal: t.enrollment_answers.scf_round_url ? "ok" : "absent",
      value: t.enrollment_answers.scf_round_url ?? "None given",
    },
  ];
  if (prior.length > 0)
    items.push({
      key: "prior",
      label: "Prior domain",
      signal: "risk",
      value: `${prior.map((s) => s.domain).join(", ")} belonged to a removed project`,
    });
  items.push({
    key: "events",
    label: "Events, 30 days",
    signal: accepted > 0 ? "ok" : "absent",
    value:
      events === 0
        ? "None yet"
        : `${formatCount(accepted)} accepted of ${formatCount(events)} received`,
  });
  return items;
}

export function EvidenceList({ items }: { items: EvidenceItem[] }) {
  return (
    <ul aria-label="Evidence" className="flex flex-col">
      {items.map((e) => (
        <li
          key={e.key}
          className="grid grid-cols-[1rem_minmax(0,1fr)] items-start gap-x-2.5 py-1.5 text-small sm:grid-cols-[1rem_10rem_minmax(0,1fr)]"
        >
          <span className="mt-0.5 flex">{SIGNAL_ICON[e.signal]}</span>
          <span className="text-fg-2">
            {e.label}
            <span className="sr-only">, {SIGNAL_WORD[e.signal]}:</span>
          </span>
          <span
            className={cn(
              "col-start-2 min-w-0 break-words sm:col-start-3",
              e.signal === "absent" ? "text-fg-2" : "text-fg",
            )}
          >
            {e.value}
          </span>
        </li>
      ))}
    </ul>
  );
}

// ---- the decision

type Choice = "approve" | "reject";

// presets fill the text the members read; the operator edits it afterwards
const PRESETS = [
  "Not built on Stellar.",
  "No Stellar integration shown yet.",
  "Ownership of the website not verified.",
  "Duplicate of another project.",
];

// approve or reject a pending project inline. a reject needs a reason, an
// approve takes an optional note (contract 5.5); both reach the members.
export function DecisionPanel({
  detail,
  onDecided,
}: {
  detail: OperatorTenantDetail;
  onDecided?: (choice: Choice) => void;
}) {
  const t = detail.tenant;
  const allowed = (["approve", "reject"] as const).filter((a) =>
    detail.allowed_actions.includes(a),
  );
  const [choice, setChoice] = useState<Choice>(allowed[0] ?? "approve");
  const [reason, setReason] = useState("");
  const [fieldError, setFieldError] = useState<string | null>(null);
  const ids = useId();
  const run = useMutation({
    mutationFn: ({ act, text }: { act: Choice; text: string }) =>
      api.operator.tenantAction(t.id, act, text),
    onSuccess: async (_d, vars) => {
      onDecided?.(vars.act);
      await refreshOperator();
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

  // a and r pick, ctrl or cmd enter submits; typing in the reason is left alone
  function keys(e: KeyboardEvent) {
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
      onKeyDown={keys}
      noValidate
      className="flex flex-col gap-3"
    >
      <div role="radiogroup" aria-label="Decision" className="grid grid-cols-2 gap-2">
        {allowed.map((c) => {
          const on = c === choice;
          return (
            // biome-ignore lint/a11y/useSemanticElements: a radio card with a shortcut hint, like the segmented control
            <button
              key={c}
              type="button"
              role="radio"
              aria-checked={on}
              aria-keyshortcuts={c === "approve" ? "A" : "R"}
              onClick={() => pick(c)}
              className={cn(
                "flex items-center justify-between gap-2 rounded-md border px-3 py-2.5 text-left text-body font-medium transition-colors duration-(--vs-duration-press) ease-standard focus-ring max-lg:min-h-(--vs-control-touch)",
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
                  "h-7 rounded-full border px-2.5 text-caption transition-colors duration-(--vs-duration-press) ease-standard focus-ring max-lg:h-9",
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

      <div className="flex flex-col gap-1.5">
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
          className="flex animate-fade-in flex-wrap items-start gap-2.5 rounded-md border border-line-strong px-3 py-2.5 text-small text-fg"
        >
          <CircleAlert aria-hidden className="mt-0.5 size-4 shrink-0 text-warning" />
          <span className="min-w-0 flex-1">
            Someone decided first. {run.error instanceof Error ? run.error.message : null}
          </span>
          <Button type="button" variant="outline" size="xs" onClick={() => refreshOperator()}>
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
              ? "Reject project"
              : "Approve project"}
          <span aria-hidden className="max-lg:hidden">
            <Kbd>Ctrl Enter</Kbd>
          </span>
        </Button>
      </div>
    </form>
  );
}

export function Waiting({ since, now }: { since: string; now: number }) {
  return (
    <span className="flex items-center gap-1.5 text-caption text-warning">
      <Clock aria-hidden className="size-3" strokeWidth={2.4} />
      {waitingText(since, now)}
    </span>
  );
}

export function enrolledLine(t: OperatorTenantDetail["tenant"]): string {
  return `Enrolled ${formatDate(t.created_at)}${t.created_by ? ` by ${t.created_by.github_login}` : ""}.`;
}
