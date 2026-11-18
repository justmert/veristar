// the one evidence list: the facts a decision on a project rests on, each
// with one status (proven, partial, failed, not given). the review queue,
// the operator project page and the scf review read it the same way.

import { cn } from "@veristar/brand/cn";
import { CircleAlert, CircleCheck, CircleDashed, CircleX } from "lucide-react";
import type { ReactNode } from "react";
import type { IntegrityGrade, OperatorTenantDetail } from "@/api/schemas";
import { countOf, formatCount } from "@/lib/format";

// ok: the fact is proven. partial: part of it is proven, or it is a risk.
// missing: the project claims it and the check failed. absent: nothing was
// given to check.
export type Signal = "ok" | "partial" | "missing" | "absent";

const SIGNAL_ICON: Record<Signal, ReactNode> = {
  ok: <CircleCheck aria-hidden className="size-3.5 text-success" strokeWidth={2.4} />,
  partial: <CircleAlert aria-hidden className="size-3.5 text-warning" strokeWidth={2.4} />,
  missing: <CircleX aria-hidden className="size-3.5 text-critical" strokeWidth={2.4} />,
  absent: <CircleDashed aria-hidden className="size-3.5 text-fg-3" strokeWidth={2.4} />,
};

const VALUE_TONE: Record<Signal, string> = {
  ok: "text-fg",
  partial: "text-warning",
  missing: "text-fg",
  absent: "text-fg-2",
};

export type EvidenceItem = { key: string; label: string; value: ReactNode; signal: Signal };

// a part of the sites: none, some or all of them
function share(part: number, all: number): Signal {
  if (part === 0) return "missing";
  return part < all ? "partial" : "ok";
}

// the facts the decision rests on. each item has one status: an icon, a
// colour and a value that says the state in words, so the status never
// depends on the icon or on colour alone.
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
  const of = (n: number) => `${formatCount(n)} of ${countOf(sites.length, "site")}`;
  const items: EvidenceItem[] = [
    sites.length === 0
      ? { key: "domain", label: "Domain", signal: "absent", value: "No site yet" }
      : {
          key: "domain",
          label: "Domain",
          signal: share(verified.length, sites.length),
          value:
            verified.length === sites.length
              ? `Verified (${of(verified.length)})`
              : verified.length === 0
                ? `Not verified (${of(0)})`
                : `${of(verified.length)} verified`,
        },
    sites.length === 0
      ? { key: "toml", label: "stellar.toml", signal: "absent", value: "No site to check" }
      : {
          key: "toml",
          label: "stellar.toml",
          signal: share(toml.length, sites.length),
          value:
            toml.length === 0
              ? `Not found (${of(0)})`
              : `Found on ${toml.map((s) => s.domain).join(", ")} (${of(toml.length)})`,
        },
    {
      key: "ids",
      label: "IDs given at enrollment",
      signal: ids.length === 0 ? "absent" : invalid.length > 0 ? "partial" : "ok",
      value:
        ids.length === 0
          ? "Not given"
          : invalid.length > 0
            ? `${invalid.length} of ${ids.length} not a valid ID`
            : `Valid (${countOf(ids.length, "ID")})`,
    },
    {
      key: "scf",
      label: "SCF round link",
      signal: t.enrollment_answers.scf_round_url ? "ok" : "absent",
      value: t.enrollment_answers.scf_round_url ?? "Not given",
    },
  ];
  if (prior.length > 0)
    items.push({
      key: "prior",
      label: "Prior domain",
      signal: "partial",
      value: `${prior.map((s) => s.domain).join(", ")} belonged to a removed project`,
    });
  items.push({
    key: "events",
    label: "Events, last 30 full days",
    signal: accepted > 0 ? "ok" : events > 0 ? "missing" : "absent",
    value:
      events === 0
        ? "No events yet"
        : `${formatCount(accepted)} accepted of ${formatCount(events)} received`,
  });
  if (grade !== undefined && grade !== null)
    items.push({
      key: "grade",
      label: "Integrity grade",
      signal: grade.grade === "A" ? "ok" : grade.grade === null ? "absent" : "partial",
      value: grade.grade === null ? "Not graded" : `Grade ${grade.grade}`,
    });
  return items;
}

// one layout everywhere: each fact on one line, its status icon, its label
// and its value. the labels share one column as wide as the longest, so
// the values line up. `stacked` is kept for callers and changes nothing.
export function EvidenceList({ items }: { items: EvidenceItem[]; stacked?: boolean }) {
  return (
    <ul
      aria-label="Evidence"
      className="grid grid-cols-[1rem_auto_minmax(0,1fr)] gap-x-3 text-small"
    >
      {items.map((e) => (
        <li
          key={e.key}
          data-evidence={e.key}
          data-signal={e.signal}
          className="col-span-3 grid grid-cols-subgrid items-baseline py-1"
        >
          <span className="flex translate-y-0.5 self-start">{SIGNAL_ICON[e.signal]}</span>
          <span className="max-w-48 text-fg-2">
            {e.label}
            <span className="sr-only">:</span>
          </span>
          <span className={cn("min-w-0 break-words", VALUE_TONE[e.signal])}>{e.value}</span>
        </li>
      ))}
    </ul>
  );
}
