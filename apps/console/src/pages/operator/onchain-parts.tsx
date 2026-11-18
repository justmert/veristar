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

// the on-chain section of the compare page: totals and one strip of daily
// operations per project
export function CompareOnchainSection({
  projects,
}: {
  projects: { tenant: TenantRef; onchain?: CompareOnchain }[];
}) {
  return (
    <section aria-labelledby="compare-onchain" className="flex min-w-0 flex-col gap-4">
      <div className="flex flex-col gap-1">
        <h2 id="compare-onchain" className="text-section text-fg">
          On-chain
        </h2>
        <p className="text-small text-fg-2">
          Operations of the declared accounts and contracts per UTC day. A dashed day has no data.
        </p>
      </div>
      <ul className="flex flex-col gap-4 rounded-lg border border-line bg-raised p-4 sm:p-5">
        {projects.map((p, i) => {
          const s = p.onchain;
          const declared = !!s && s.totals.ids > 0;
          return (
            <li key={p.tenant.id} data-tenant={p.tenant.id} className="flex min-w-0 flex-col gap-2">
              <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
                <SeriesSwatch slot={i} />
                <span className="min-w-0 truncate text-body font-medium text-fg">
                  {p.tenant.name}
                </span>
                {declared ? (
                  <span className="text-small text-fg-2">
                    <span className="text-fg tabular">{formatCount(s.totals.operations)}</span>{" "}
                    operations, {countOf(s.totals.transactions, "transaction")},{" "}
                    {s.totals.ids_verified} of {countOf(s.totals.ids, "id")} verified. Weekly active
                    accounts: {weeklyActiveText(s.totals.weekly_active_accounts)}.
                  </span>
                ) : (
                  <span className="text-small text-fg-2">No accounts or contracts declared</span>
                )}
              </div>
              {declared ? (
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

// every declared id of one project with its daily strip, for the operator
// project page. the default range of the on-chain view.
export function TenantOnchainSection({ tenantId }: { tenantId: string }) {
  const q = useQuery(operatorOnchainQuery(tenantId, {}));
  if (q.isPending)
    return (
      <LoadingState label="Loading the on-chain ids">
        <Skeleton.Table rows={2} columns={2} />
      </LoadingState>
    );
  if (q.error)
    return (
      <ErrorState
        compact
        title="The on-chain ids did not load"
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
