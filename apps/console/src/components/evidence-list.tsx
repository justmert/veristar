// the one evidence list: the facts a decision on a project rests on, each
// with its signal (proven, risk, absent). the review queue, the operator
// project page and the scf review read it the same way.

import { cn } from "@veristar/brand/cn";
import { Check, CircleAlert, Minus } from "lucide-react";
import type { ReactNode } from "react";
import type { IntegrityGrade, OperatorTenantDetail } from "@/api/schemas";
import { countOf, formatCount } from "@/lib/format";

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
export function evidenceOf(
  detail: OperatorTenantDetail,
  grade?: { grade: IntegrityGrade | null } | null,
): EvidenceItem[] {
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
          : `${verified.length} of ${countOf(sites.length, "site")} verified`,
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
      label: "Ids given at enrollment",
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
      label: "SCF round link",
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
    label: "Events, last 30 full days",
    signal: accepted > 0 ? "ok" : "absent",
    value:
      events === 0
        ? "None yet"
        : `${formatCount(accepted)} accepted of ${formatCount(events)} received`,
  });
  if (grade !== undefined && grade !== null)
    items.push({
      key: "grade",
      label: "Integrity grade",
      signal: grade.grade === "A" ? "ok" : grade.grade === null ? "absent" : "risk",
      value: grade.grade === null ? "Not graded" : `Grade ${grade.grade}`,
    });
  return items;
}

// stacked puts each value under its label, for a narrow column
export function EvidenceList({ items, stacked }: { items: EvidenceItem[]; stacked?: boolean }) {
  return (
    <ul aria-label="Evidence" className="flex flex-col">
      {items.map((e) => (
        <li
          key={e.key}
          className={cn(
            "grid grid-cols-[1rem_minmax(0,1fr)] items-start gap-x-2 py-1 text-small",
            !stacked && "sm:grid-cols-[1rem_10rem_minmax(0,1fr)]",
          )}
        >
          <span className="mt-1 flex">{SIGNAL_ICON[e.signal]}</span>
          <span className="text-fg-2">
            {e.label}
            <span className="sr-only">, {SIGNAL_WORD[e.signal]}:</span>
          </span>
          <span
            className={cn(
              "col-start-2 min-w-0 break-words",
              !stacked && "sm:col-start-3",
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
