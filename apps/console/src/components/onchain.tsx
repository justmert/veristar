import { cn } from "@veristar/brand/cn";
import { Link2, Link2Off } from "lucide-react";
import type { ReactNode } from "react";
import type { ChainId, OnchainView, OnchainWeek } from "@/api/onchain";
import { Missing } from "@/components/states";
import { Pill } from "@/components/status";
import { Tooltip } from "@/components/ui/tooltip";
import { countOf, formatCount, formatDateRange, formatWeekOf } from "@/lib/format";
import { METHOD_TEXT, missingReasons, missingText, shortKey } from "@/lib/onchain";

// the parts of the on-chain witness that the on-chain page and the operator
// project page share. a day without a stored row is drawn as missing with
// its reason, never as zero.

export function VerifiedPill({ id }: { id: ChainId }) {
  const v = id.verification;
  if (v.status === "verified") {
    const pill = <Pill tone="success">Verified</Pill>;
    return v.method ? (
      <Tooltip content={`Proven by ${METHOD_TEXT[v.method].toLowerCase()}`}>
        {/* biome-ignore lint/a11y/noNoninteractiveTabindex: the method opens on focus too */}
        <span tabIndex={0} className="rounded-full focus-ring">
          {pill}
        </span>
      </Tooltip>
    ) : (
      pill
    );
  }
  // proving control is the builder's next step, so it needs action
  return <Pill tone="warning">Unverified</Pill>;
}

export function KindLabel({ kind }: { kind: ChainId["kind"] }) {
  return (
    <span className="text-caption text-fg-2">{kind === "account" ? "Account" : "Contract"}</span>
  );
}

// a 56-character id in its short form, the whole id in a tooltip and for
// screen readers. the same form on every page.
export function StrKey({ strkey, className }: { strkey: string; className?: string }) {
  return (
    <Tooltip content={<span className="break-all font-mono">{strkey}</span>}>
      {/* biome-ignore lint/a11y/noNoninteractiveTabindex: the whole id opens on focus too */}
      <span tabIndex={0} className={cn("rounded-sm focus-ring", className)}>
        <code data-strkey={strkey} className="font-mono text-small text-fg">
          <span aria-hidden>{shortKey(strkey)}</span>
          <span className="sr-only">{strkey}</span>
        </code>
      </span>
    </Tooltip>
  );
}

// one cell per utc day: a bar for a read day (a zero is a flat bar), a
// dashed empty cell for a day without numbers, with its reason on hover
export function DayStrip({
  points,
  label,
  marker,
}: {
  points: { day: string; value: number | null; missing?: string | null }[];
  label: string;
  // a day to annotate, such as the project's award date (contract 19.5)
  marker?: { day: string; label: string } | null;
}) {
  const max = Math.max(1, ...points.map((p) => p.value ?? 0));
  return (
    <div role="img" aria-label={label} className="flex h-10 items-end gap-px">
      {points.map((p) => {
        const marked = marker?.day === p.day;
        const base =
          p.value === null
            ? `${p.day}: no data. ${p.missing ? missingText(p.missing) : ""}`
            : `${p.day}: ${formatCount(p.value)}`;
        const title = marked ? `${base}. ${marker?.label}` : base;
        return (
          <span
            key={p.day}
            title={title}
            data-marker={marked ? p.day : undefined}
            className={cn(
              "flex h-full min-w-0 flex-1 items-end",
              marked && "border-l-2 border-dashed border-accent",
            )}
          >
            {p.value === null ? (
              <span className="h-full w-full rounded-bar border border-dashed border-line-strong" />
            ) : (
              <span
                className="w-full rounded-bar bg-series-1"
                style={{ height: `${Math.max(4, (p.value / max) * 100)}%` }}
              />
            )}
          </span>
        );
      })}
    </div>
  );
}

// weekly active accounts by the scf growth hack definition: the distinct
// accounts that initiated at least one operation involving the project's
// verified accounts or contracts in an iso week (utc). a week without a
// count shows the missing glyph and its reason, never a zero.
export function WeeklyActiveList({ weeks, label }: { weeks: OnchainWeek[]; label: string }) {
  if (weeks.length === 0) {
    return <p className="text-small text-fg-2">No complete ISO week in this range.</p>;
  }
  // when every week misses for the same reason, the reason shows once
  // under the weeks instead of in each of them
  const codes = new Set(
    weeks.map((w) => (w.active_accounts === null ? (w.missing ?? "week_not_counted") : "")),
  );
  const shared = codes.size === 1 && !codes.has("") ? [...codes][0] : null;
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <ol aria-label={label} className="grid grid-cols-2 gap-2 sm:grid-cols-4">
        {weeks.map((w) => {
          const reason =
            w.active_accounts === null ? missingText(w.missing ?? "week_not_counted") : "";
          return (
            <li
              key={w.week}
              data-week={w.week}
              className="flex min-w-0 flex-col gap-1 rounded-md border border-line bg-sunken px-3 py-2"
            >
              <span className="text-caption text-fg-2" title={w.week}>
                {formatWeekOf(w.from)}
                <span className="sr-only">, {formatDateRange(w.from, w.to)}, UTC</span>
              </span>
              {w.active_accounts === null ? (
                <>
                  <Missing reason={reason} className="text-title" />
                  {shared ? null : (
                    <span
                      aria-hidden
                      className="text-caption text-fg-2"
                      data-missing={w.missing ?? ""}
                    >
                      {reason}
                    </span>
                  )}
                </>
              ) : (
                <span className="text-title text-fg tabular">{formatCount(w.active_accounts)}</span>
              )}
            </li>
          );
        })}
      </ol>
      {shared ? (
        <p className="text-caption text-fg-2" data-missing={shared}>
          {/* said once for every week, so it names them all */}
          {missingText(shared).replace(/for this week$/, "for any week in this range")}.
        </p>
      ) : null}
    </div>
  );
}

export function LinkStatus({ view }: { view: OnchainView }) {
  return view.link.two_way ? (
    <Pill tone="success">
      <Link2 aria-hidden className="size-3" />
      stellar.toml linked both ways
    </Pill>
  ) : (
    <Pill tone="neutral">
      <Link2Off aria-hidden className="size-3" />
      stellar.toml link not complete
    </Pill>
  );
}

// one id's totals over the range in one line
export function IdTotals({ id }: { id: ChainId }) {
  // without a read day the totals are not measured, so no 0 shows
  if (id.totals.days_read === 0) {
    return <span className="text-small text-fg-2">No ledger read in this range</span>;
  }
  return (
    <span className="text-small text-fg-2">
      <span className="text-fg tabular">{formatCount(id.totals.operations)}</span>{" "}
      {id.totals.operations === 1 ? "operation" : "operations"},{" "}
      {countOf(id.totals.transactions, "transaction")}, peak{" "}
      {countOf(id.totals.peak_counterparties, "counterparty", "counterparties")} a day
    </span>
  );
}

// why an id has days without numbers, one line per reason
export function MissingDays({ id }: { id: ChainId }) {
  const reasons = missingReasons(id);
  if (reasons.length === 0) return null;
  return (
    <ul className="flex flex-col gap-1 text-caption text-fg-2">
      {reasons.map((r) => (
        <li key={r.code} data-missing-reason={r.code}>
          {countOf(r.days, "day")} without data. {missingText(r.code)}
        </li>
      ))}
    </ul>
  );
}

// the per-id rows of a view: kind, id, proof, numbers and why any day is
// missing. extra adds a page's own part under an id, such as a contract's
// calls per function
export function IdRows({ view, extra }: { view: OnchainView; extra?: (id: ChainId) => ReactNode }) {
  return (
    <ul className="flex flex-col divide-y divide-line">
      {view.ids.map((id) => (
        <li key={id.id} className="flex flex-col gap-3 py-4 first:pt-0 last:pb-0">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
            <KindLabel kind={id.kind} />
            <StrKey strkey={id.strkey} />
            <VerifiedPill id={id} />
            <span className="sm:ml-auto">
              <IdTotals id={id} />
            </span>
          </div>
          <DayStrip
            label={`Daily operations of ${id.strkey}`}
            points={id.series.map((p) => ({ day: p.day, value: p.operations, missing: p.missing }))}
          />
          <MissingDays id={id} />
          {extra ? extra(id) : null}
        </li>
      ))}
    </ul>
  );
}
