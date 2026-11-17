import { useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { ArrowRight, Download, Link2, Link2Off } from "lucide-react";
import type { ReactNode } from "react";
import { Link } from "react-router";
import {
  type ChainId,
  exportPaths,
  type OnchainView,
  type OnchainWeek,
  onchainQuery,
} from "@/api/onchain";
import { ErrorPanel, Loading } from "@/components/error-panel";
import { Pill } from "@/components/status";
import { Button } from "@/components/ui/button";
import { formatCount } from "@/lib/format";
import { formatDayRange } from "@/lib/global";
import {
  dailyOperations,
  METHOD_TEXT,
  missingReasons,
  missingText,
  shortKey,
  weeklyActiveText,
} from "@/lib/onchain";
import { lastDaysOf } from "@/lib/web-dashboard";

// the on-chain witness in the console: the project panel on the overview,
// and the parts the operator detail and the management page share. a day
// without a stored row is drawn as missing with its reason, never as zero.

export function VerifiedPill({ id }: { id: ChainId }) {
  const v = id.verification;
  if (v.status === "verified") {
    return (
      <span title={v.method ? METHOD_TEXT[v.method] : undefined}>
        <Pill tone="success">Verified</Pill>
      </span>
    );
  }
  return <Pill tone="warning">Unverified</Pill>;
}

export function KindLabel({ kind }: { kind: ChainId["kind"] }) {
  return (
    <span className="text-caption text-fg-3">{kind === "account" ? "Account" : "Contract"}</span>
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
// count shows its reason and is never drawn as zero.
export function WeeklyActiveList({ weeks, label }: { weeks: OnchainWeek[]; label: string }) {
  if (weeks.length === 0) {
    return <p className="text-small text-fg-3">No complete ISO week in this range.</p>;
  }
  return (
    <ol aria-label={label} className="grid grid-cols-2 gap-2 sm:grid-cols-4">
      {weeks.map((w) => (
        <li
          key={w.week}
          data-week={w.week}
          className="flex min-w-0 flex-col gap-0.5 rounded-md border border-line bg-sunken px-3 py-2"
          title={`${w.from} to ${w.to} (UTC)`}
        >
          <span className="text-caption text-fg-3">{w.week}</span>
          {w.active_accounts === null ? (
            <span className="text-small text-fg-2" data-missing={w.missing ?? ""}>
              No count. {missingText(w.missing ?? "week_not_counted")}
            </span>
          ) : (
            <span className="text-title text-fg tabular-nums">
              {formatCount(w.active_accounts)}
            </span>
          )}
        </li>
      ))}
    </ol>
  );
}

function Figure({ label, value, hint }: { label: string; value: ReactNode; hint?: string }) {
  return (
    <div className="flex min-w-0 flex-col gap-0.5">
      <span className="text-caption text-fg-3">{label}</span>
      <span className="text-title text-fg tabular-nums">{value}</span>
      {hint ? <span className="text-caption text-fg-3">{hint}</span> : null}
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
    <Pill tone="info">
      <Link2Off aria-hidden className="size-3" />
      stellar.toml link not complete
    </Pill>
  );
}

// the per-id rows of a view: kind, strkey, proof, numbers and why any day
// is missing
export function IdRows({ view, compact }: { view: OnchainView; compact?: boolean }) {
  return (
    <ul className="flex flex-col divide-y divide-line">
      {view.ids.map((id) => {
        const reasons = missingReasons(id);
        return (
          <li key={id.id} className="flex flex-col gap-2 py-3 first:pt-0 last:pb-0">
            <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
              <KindLabel kind={id.kind} />
              <code className="min-w-0 truncate text-small text-fg" title={id.strkey}>
                {compact ? shortKey(id.strkey) : id.strkey}
              </code>
              <VerifiedPill id={id} />
              <span className="ml-auto text-small text-fg-2 tabular-nums">
                {formatCount(id.totals.operations)} operations ·{" "}
                {formatCount(id.totals.transactions)} transactions · peak{" "}
                {formatCount(id.totals.peak_counterparties)} counterparties a day
              </span>
            </div>
            <DayStrip
              label={`Daily operations of ${id.strkey}`}
              points={id.series.map((p) => ({
                day: p.day,
                value: p.operations,
                missing: p.missing,
              }))}
            />
            {reasons.length ? (
              <ul className="flex flex-col gap-0.5 text-caption text-fg-2">
                {reasons.map((r) => (
                  <li key={r.code}>
                    {r.days} {r.days === 1 ? "day" : "days"} without data: {missingText(r.code)}
                  </li>
                ))}
              </ul>
            ) : null}
          </li>
        );
      })}
    </ul>
  );
}

// the "on-chain" panel of the project overview
export function OnchainPanel({
  view,
  manageTo,
  actions,
  rangeLabel,
}: {
  view: OnchainView;
  manageTo?: string;
  actions?: ReactNode;
  // the utc days the panel covers, when a page lets people choose them
  rangeLabel?: string;
}) {
  const t = view.totals;
  const daily = dailyOperations(view);
  return (
    <section
      aria-labelledby="onchain-h"
      className="flex min-w-0 flex-col gap-4 rounded-lg border border-line bg-raised px-4 py-3.5 sm:px-5"
    >
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex min-w-0 flex-col gap-0.5">
          <h2 id="onchain-h" className="text-title text-fg">
            On-chain
          </h2>
          <p className="text-small text-fg-2">
            Ledger activity of the accounts and contracts the project declared, per UTC day.
          </p>
          {rangeLabel ? (
            <p className="text-small text-fg-2" data-testid="onchain-range">
              {rangeLabel}
            </p>
          ) : null}
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {t.ids > 0 ? <LinkStatus view={view} /> : null}
          {actions}
        </div>
      </div>
      {t.ids === 0 ? (
        <p className="text-body text-fg-2">
          No accounts or contracts declared yet. Add the ones the project operates so its ledger
          activity shows next to its web and API usage.
        </p>
      ) : (
        <>
          <dl className="grid grid-cols-2 gap-4 sm:grid-cols-4">
            <Figure label="Operations" value={formatCount(t.operations)} />
            <Figure label="Transactions" value={formatCount(t.transactions)} />
            <Figure
              label="Verified"
              value={`${t.ids_verified} of ${t.ids}`}
              hint="accounts and contracts"
            />
            <Figure
              label="Days without data"
              value={formatCount(t.days_missing)}
              hint="summed over ids, today excluded"
            />
          </dl>
          <DayStrip label="Daily operations of the project" points={daily} />
          <div className="flex flex-col gap-2">
            <div className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5">
              <h3 className="text-body font-medium text-fg">Weekly active accounts</h3>
              <span className="text-small text-fg-2" data-testid="waa-summary">
                {weeklyActiveText(t.weekly_active_accounts)}
              </span>
            </div>
            <p className="max-w-prose text-caption text-fg-3">
              Accounts that initiated at least one operation involving your verified accounts or
              contracts in an ISO week (UTC), as the SCF Growth Hack defines an active account.
            </p>
            <WeeklyActiveList weeks={view.weeks} label="Weekly active accounts per ISO week" />
          </div>
          <IdRows view={view} compact />
        </>
      )}
      {manageTo ? (
        <Link
          to={manageTo}
          className={cn(
            "inline-flex w-fit items-center gap-1 rounded-sm text-small text-fg-2 transition-colors duration-(--vs-duration-press) hover:text-fg focus-ring",
          )}
        >
          Manage accounts and contracts
          <ArrowRight aria-hidden className="size-3" />
        </Link>
      ) : null}
    </section>
  );
}

// the on-chain section of the overview and the web dashboard: the panel over
// the page's range of utc days, with the export of its daily rows. a range
// is the page's own choice; days counts back from today.
export function ProjectOnchainSection({
  tenantId,
  days,
  range: chosen,
}: {
  tenantId: string;
  days?: number;
  range?: { from: string; to: string };
}) {
  const range = chosen ? lastDaysOf(chosen, ONCHAIN_MAX_DAYS) : lastUtcDays(days ?? 30);
  const q = useQuery(onchainQuery(tenantId, range));
  if (q.isPending) return <Loading label="Loading on-chain activity" />;
  if (q.error) return <ErrorPanel error={q.error} onRetry={() => q.refetch()} />;
  return (
    <OnchainPanel
      view={q.data}
      manageTo={`/projects/${tenantId}/onchain`}
      rangeLabel={chosen ? onchainRangeLabel(chosen, range) : undefined}
      actions={
        <Button asChild variant="outline" size="sm">
          <a href={exportPaths.overview(tenantId, range)} download>
            <Download aria-hidden />
            Export CSV
          </a>
        </Button>
      }
    />
  );
}

function lastUtcDays(days: number): { from: string; to: string } {
  const to = new Date().toISOString().slice(0, 10);
  const from = new Date(Date.now() - (days - 1) * 86_400_000).toISOString().slice(0, 10);
  return { from, to };
}

// the on-chain views cover at most this many days (contract 15)
export const ONCHAIN_MAX_DAYS = 366;

export function onchainRangeLabel(
  chosen: { from: string; to: string },
  shown: { from: string; to: string },
): string {
  const span = formatDayRange(shown.from, shown.to);
  return shown.from === chosen.from
    ? span
    : `${span}, the last ${ONCHAIN_MAX_DAYS} days of the chosen range`;
}
