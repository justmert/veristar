import { useQuery } from "@tanstack/react-query";
import { Download } from "lucide-react";
import type { ReactNode } from "react";
import {
  type CompareOnchain,
  exportPaths,
  type OnchainSummary,
  operatorOnchainQuery,
} from "@/api/onchain";
import type { TenantRef } from "@/api/schemas";
import { SeriesLine } from "@/components/compare-charts";
import { ErrorPanel, Loading } from "@/components/error-panel";
import { DayStrip, IdRows, LinkStatus, WeeklyActiveList } from "@/components/onchain";
import { Button } from "@/components/ui/button";
import { formatCount } from "@/lib/format";
import { type ListState, listParams } from "@/lib/global";
import { weeklyActiveText } from "@/lib/onchain";
import { Missing, TD, TH } from "./global-shared";

// the operator's on-chain parts (contract 15.7) and the csv exports
// (contract 16.1), kept out of the shared table files

export function OnchainHeader() {
  return (
    <th
      scope="col"
      className={`${TH} text-right`}
      title="Operations of the declared accounts and contracts"
    >
      On-chain
    </th>
  );
}

export function OnchainCell({ s }: { s: OnchainSummary | undefined }) {
  if (!s || s.ids === 0) {
    return (
      <td className={`${TD} text-right`} data-col="onchain">
        <Missing />
      </td>
    );
  }
  const note = [
    `${s.ids_verified} of ${s.ids} ids verified`,
    `${formatCount(s.transactions)} transactions`,
    s.days_missing ? `${s.days_missing} id-days without data` : null,
    s.ids_failing ? `${s.ids_failing} ids failing to read` : null,
    `weekly active accounts: ${weeklyActiveText(s.weekly_active_accounts)}`,
  ]
    .filter(Boolean)
    .join(", ");
  return (
    <td className={`${TD} text-right`} data-col="onchain" title={note}>
      <span className="text-fg">{formatCount(s.operations)}</span>
      <span className="block text-caption text-fg-3">
        {s.ids_verified}/{s.ids} verified{s.days_missing ? `, ${s.days_missing} missing` : ""}
      </span>
      {s.weekly_active_accounts.average !== null ? (
        <span className="block text-caption text-fg-3" data-col="waa">
          {formatCount(Math.round(s.weekly_active_accounts.average))} active a week
        </span>
      ) : null}
    </td>
  );
}

// exports exactly the rows on screen: the list's query with the resolved
// range, and as many rows as are loaded
export function ExportListButton<S extends string>({
  view,
  state,
  resolved,
  rows,
}: {
  view: "web" | "api";
  state: ListState<S>;
  resolved: { from: string; to: string } | null;
  rows: number;
}) {
  if (!resolved || rows === 0) return null;
  const q = new URLSearchParams(listParams(state));
  q.set("from", resolved.from);
  q.set("to", resolved.to);
  return (
    <Button asChild variant="outline" size="sm">
      <a href={exportPaths.list(view, q, rows)} download>
        <Download aria-hidden />
        Export CSV
      </a>
    </Button>
  );
}

export function ExportCompareButton({
  view,
  tenants,
  resolved,
  children,
}: {
  view: "web" | "api";
  tenants: string[];
  resolved: { from: string; to: string } | null;
  children: ReactNode;
}) {
  if (!resolved || tenants.length === 0) return null;
  return (
    <Button asChild variant="outline" size="sm">
      <a href={exportPaths.compare(view, tenants, resolved)} download>
        <Download aria-hidden />
        {children}
      </a>
    </Button>
  );
}

// the on-chain section of the compare page: totals and one strip of daily
// operations per project
export function CompareOnchainSection({
  projects,
}: {
  projects: { tenant: TenantRef; onchain?: CompareOnchain }[];
}) {
  return (
    <section aria-labelledby="compare-onchain" className="flex flex-col gap-3">
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <h2 id="compare-onchain" className="text-section text-fg">
          On-chain
        </h2>
        <span className="text-small text-fg-3">
          Operations of the declared accounts and contracts per UTC day; a dashed day has no data
        </span>
      </div>
      <ul className="flex flex-col gap-3 rounded-lg border border-line bg-raised p-4">
        {projects.map((p, i) => {
          const s = p.onchain;
          return (
            <li key={p.tenant.id} className="flex flex-col gap-2">
              <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
                <SeriesLine slot={i} />
                <span className="text-body font-medium text-fg">{p.tenant.name}</span>
                {s && s.totals.ids > 0 ? (
                  <span className="text-small text-fg-2 tabular-nums">
                    {formatCount(s.totals.operations)} operations ·{" "}
                    {formatCount(s.totals.transactions)} transactions · {s.totals.ids_verified} of{" "}
                    {s.totals.ids} verified · weekly active accounts{" "}
                    {weeklyActiveText(s.totals.weekly_active_accounts)}
                  </span>
                ) : (
                  <span className="text-small text-fg-3">No accounts or contracts declared</span>
                )}
              </div>
              {s && s.totals.ids > 0 ? (
                <>
                  <DayStrip
                    label={`Daily operations of ${p.tenant.name}`}
                    points={s.series.map((d) => ({
                      day: d.day,
                      value: d.operations,
                      missing: d.operations === null ? "not_read_yet" : null,
                    }))}
                  />
                  <WeeklyActiveList
                    weeks={s.weeks}
                    label={`Weekly active accounts of ${p.tenant.name}`}
                  />
                </>
              ) : null}
            </li>
          );
        })}
      </ul>
    </section>
  );
}

// the on-chain section of the operator's tenant detail
export function TenantOnchainSection({ tenantId }: { tenantId: string }) {
  const q = useQuery(operatorOnchainQuery(tenantId, {}));
  return (
    <section aria-labelledby="onchain-detail-h" className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 id="onchain-detail-h" className="text-section text-fg">
          On-chain
        </h2>
        {q.data?.ids.length ? <LinkStatus view={q.data} /> : null}
      </div>
      {q.isPending ? <Loading label="Loading on-chain activity" /> : null}
      {q.error ? <ErrorPanel error={q.error} onRetry={() => q.refetch()} /> : null}
      {q.data ? (
        q.data.ids.length === 0 ? (
          <p className="text-body text-fg-2">The project declares no accounts or contracts.</p>
        ) : (
          <div className="rounded-lg border border-line bg-raised p-4">
            <p className="mb-3 text-small text-fg-2">
              {q.data.from} to {q.data.to}: {formatCount(q.data.totals.operations)} operations,{" "}
              {formatCount(q.data.totals.transactions)} transactions, {q.data.totals.ids_verified}{" "}
              of {q.data.totals.ids} ids verified. Weekly active accounts:{" "}
              {weeklyActiveText(q.data.totals.weekly_active_accounts)}.
            </p>
            <div className="mb-4">
              <WeeklyActiveList weeks={q.data.weeks} label="Weekly active accounts per ISO week" />
            </div>
            <IdRows view={q.data} />
          </div>
        )
      ) : null}
    </section>
  );
}
