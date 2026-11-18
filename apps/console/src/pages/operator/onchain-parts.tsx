import { useQuery } from "@tanstack/react-query";
import { type CompareOnchain, operatorOnchainQuery } from "@/api/onchain";
import type { TenantRef } from "@/api/schemas";
import { DayStrip, IdRows, LinkStatus, WeeklyActiveList } from "@/components/onchain";
import { ContractFunctionsBlock } from "@/components/onchain-functions";
import { ErrorState, LoadingState, Skeleton } from "@/components/states";
import { countOf, formatCount, formatDateRange } from "@/lib/format";
import { weeklyActiveText } from "@/lib/onchain";
import { SeriesSwatch } from "./shared";

// the operator's on-chain parts (contract 15.7): the compare section and the
// per id detail of a project

// the on-chain section of the compare page: the totals and a strip of
// daily operations for each project with a verified id, and one row for
// every project without on-chain data, so empty strips are never drawn
export function CompareOnchainSection({
  projects,
}: {
  projects: { tenant: TenantRef; onchain?: CompareOnchain }[];
}) {
  const slotted = projects.map((p, i) => ({ ...p, slot: i }));
  const measured = slotted.filter((p) => (p.onchain?.totals.ids_verified ?? 0) > 0);
  const none = slotted.filter((p) => (p.onchain?.totals.ids_verified ?? 0) === 0);
  return (
    <section aria-labelledby="compare-onchain" className="flex min-w-0 flex-col gap-4">
      <h2 id="compare-onchain" className="text-section text-fg">
        On-chain
      </h2>
      <ul className="flex flex-col divide-y divide-line-subtle rounded-lg border border-line bg-raised">
        {measured.map((p) => {
          const s = p.onchain as CompareOnchain;
          return (
            <li
              key={p.tenant.id}
              data-tenant={p.tenant.id}
              className="flex min-w-0 flex-col gap-2 px-4 py-4 sm:px-5"
            >
              <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
                <SeriesSwatch slot={p.slot} />
                <span className="min-w-0 truncate text-body font-medium text-fg">
                  {p.tenant.name}
                </span>
                <span className="text-small text-fg-2">
                  <span className="text-fg tabular">{formatCount(s.totals.operations)}</span>{" "}
                  operations, {countOf(s.totals.transactions, "transaction")},{" "}
                  {s.totals.ids_verified} of {countOf(s.totals.ids, "ID")} verified. Weekly active
                  accounts: {weeklyActiveText(s.totals.weekly_active_accounts)}.
                </span>
              </div>
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
            </li>
          );
        })}
        {none.length > 0 ? (
          <li
            data-onchain-none=""
            className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1 px-4 py-3 text-small sm:px-5"
          >
            <span className="text-fg-2">No on-chain data</span>
            {none.map((p) => {
              const ids = p.onchain?.totals.ids ?? 0;
              return (
                <span
                  key={p.tenant.id}
                  data-tenant={p.tenant.id}
                  className="inline-flex min-w-0 items-center gap-2"
                  title={
                    ids > 0
                      ? `0 of ${countOf(ids, "ID")} verified`
                      : "No accounts or contracts declared"
                  }
                >
                  <SeriesSwatch slot={p.slot} />
                  <span className="truncate text-fg">{p.tenant.name}</span>
                  {ids > 0 ? <span className="text-fg-2 tabular">0 of {ids}</span> : null}
                </span>
              );
            })}
          </li>
        ) : null}
      </ul>
    </section>
  );
}

// every declared id of one project with its daily strip, for the operator
// project page. the default range of the on-chain view.
export function TenantOnchainSection({ tenantId }: { tenantId: string }) {
  const q = useQuery(operatorOnchainQuery(tenantId, {}));
  if (q.isPending)
    return (
      <LoadingState label="Loading the on-chain IDs">
        <Skeleton.Table rows={2} columns={2} />
      </LoadingState>
    );
  if (q.error)
    return (
      <ErrorState
        compact
        title="The on-chain IDs did not load"
        error={q.error}
        onRetry={() => q.refetch()}
      />
    );
  if (q.data.ids.length === 0)
    return <p className="text-small text-fg-2">The project declares no accounts or contracts.</p>;
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <LinkStatus view={q.data} />
        <span className="text-small text-fg-2">{formatDateRange(q.data.from, q.data.to)}, UTC</span>
      </div>
      <WeeklyActiveList weeks={q.data.weeks} label="Weekly active accounts per ISO week" />
      <IdRows
        view={q.data}
        extra={(id) => (
          <ContractFunctionsBlock id={id} source={{ tenantId, operator: true, range: {} }} />
        )}
      />
    </div>
  );
}
