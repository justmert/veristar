import { Check, Minus } from "lucide-react";
import { Link } from "react-router";
import type { TenantListQuery } from "@/api/endpoints";
import type { OperatorTenantSummary } from "@/api/schemas";
import { StatusPill } from "@/components/status";
import { formatDate } from "@/lib/format";
import { ROW, SortHeader, TD, TH } from "./global-shared";

type Sort = TenantListQuery["sort"];

function Signal({ ok, label }: { ok: boolean; label: string }) {
  return (
    <span className="inline-flex items-center gap-1 whitespace-nowrap">
      {ok ? (
        <Check aria-hidden className="size-3 text-success" strokeWidth={2.8} />
      ) : (
        <Minus aria-hidden className="size-3 text-fg-3" strokeWidth={2.8} />
      )}
      {label}
    </span>
  );
}

// the project list as a table (system v2 C-ProjectTable): one row per
// project, the sortable columns are the sorts of contract 5.3
export function TenantTable({
  items,
  sort,
  order,
  onSort,
}: {
  items: OperatorTenantSummary[];
  sort: Sort;
  order: "asc" | "desc";
  onSort: (s: Sort) => void;
}) {
  return (
    <table className="w-full min-w-[52rem] border-separate border-spacing-0 text-left text-small tabular">
      <thead>
        <tr>
          <SortHeader
            label="Project"
            sort="name"
            current={sort}
            order={order}
            onSort={onSort}
            className="pl-4"
          />
          <th scope="col" className={TH}>
            Status
          </th>
          <th scope="col" className={TH}>
            Sites and evidence
          </th>
          <SortHeader
            label="Enrolled"
            sort="created_at"
            current={sort}
            order={order}
            onSort={onSort}
          />
          <SortHeader
            label="Status since"
            sort="status_changed_at"
            current={sort}
            order={order}
            onSort={onSort}
            className="pr-4"
          />
        </tr>
      </thead>
      <tbody className="[&>tr:last-child>*]:border-0">
        {items.map((t) => (
          <tr key={t.id} data-tenant={t.id} className={`group ${ROW}`}>
            <th scope="row" className={`${TD} max-w-80 pl-4 text-left font-normal`}>
              <Link
                to={`/operator/projects/${t.id}`}
                className="block truncate rounded-sm text-body font-medium text-fg underline-offset-4 group-hover:underline focus-ring"
              >
                {t.name}
              </Link>
              <span className="block truncate text-caption text-fg-3">
                <span className="font-mono">{t.slug}</span>, {t.website}
              </span>
              {t.status_reason ? (
                <span className="block truncate text-caption text-fg-2">
                  Reason: {t.status_reason}
                </span>
              ) : null}
            </th>
            <td className={TD}>
              <span className="flex flex-col items-start gap-1">
                <StatusPill status={t.status} />
                {t.sites_in_review > 0 ? (
                  <span className="text-caption whitespace-nowrap text-warning">
                    {t.sites_in_review} in review
                  </span>
                ) : null}
              </span>
            </td>
            <td className={`${TD} text-caption text-fg-2`}>
              <span className="flex flex-wrap items-center gap-x-3 gap-y-1">
                <span className="font-mono text-fg">
                  {t.site_count} {t.site_count === 1 ? "site" : "sites"}
                </span>
                <Signal ok={t.sites_verified > 0} label={`${t.sites_verified} verified`} />
                <Signal
                  ok={t.sites_with_stellar_toml > 0}
                  label={`${t.sites_with_stellar_toml} with stellar.toml`}
                />
              </span>
            </td>
            <td className={`${TD} whitespace-nowrap text-fg-2`}>{formatDate(t.created_at)}</td>
            <td className={`${TD} pr-4 whitespace-nowrap text-fg-2`}>
              {formatDate(t.status_changed_at)}
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
