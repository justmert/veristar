import { useInfiniteQuery } from "@tanstack/react-query";
import { SearchX } from "lucide-react";
import { useEffect, useState } from "react";
import { useSearchParams } from "react-router";
import type { TenantListQuery } from "@/api/endpoints";
import { operatorTenantsQuery } from "@/api/queries";
import { type TenantStatus, tenantStatus } from "@/api/schemas";
import { ErrorPanel, Loading, StateBlock } from "@/components/error-panel";
import { Button } from "@/components/ui/button";
import { formatCount } from "@/lib/format";
import { FilterChip, InlineSelect, SearchBox, TableScroll } from "./global-shared";
import { TenantTable } from "./tenant-table";

const STATUS_TEXT: Record<TenantStatus, string> = {
  pending: "Pending",
  active: "Active",
  rejected: "Rejected",
  suspended: "Suspended",
  removed: "Removed",
};

const SORTS = ["created_at", "status_changed_at", "name"] as const;
const SORT_TEXT: Record<(typeof SORTS)[number], string> = {
  created_at: "Enrollment date",
  status_changed_at: "Last status change",
  name: "Name",
};

// the filters live in the address, so a filtered list can be shared and
// survives a reload. values outside the contract's sets are dropped here
// rather than sent, because the control plane would answer 400 for them.
export function queryFromParams(p: URLSearchParams): TenantListQuery {
  const statuses = (p.get("status") ?? "")
    .split(",")
    .filter((s): s is TenantStatus => tenantStatus.safeParse(s).success);
  const sort = p.get("sort");
  const order = p.get("order");
  return {
    status: [...new Set(statuses)],
    review: p.get("review") === "true",
    q: (p.get("q") ?? "").slice(0, 100),
    sort: (SORTS as readonly string[]).includes(sort ?? "")
      ? (sort as TenantListQuery["sort"])
      : "created_at",
    order: order === "asc" ? "asc" : "desc",
  };
}

export function paramsFromQuery(q: TenantListQuery): URLSearchParams {
  const p = new URLSearchParams();
  if (q.status.length) p.set("status", q.status.join(","));
  if (q.review) p.set("review", "true");
  if (q.q.trim()) p.set("q", q.q.trim());
  if (q.sort !== "created_at") p.set("sort", q.sort);
  if (q.order !== "desc") p.set("order", q.order);
  return p;
}

export function TenantsPage() {
  const [params, setParams] = useSearchParams();
  const q = queryFromParams(params);
  const [search, setSearch] = useState(q.q);
  useEffect(() => setSearch(q.q), [q.q]);
  const list = useInfiniteQuery(operatorTenantsQuery(q));
  const items = list.data?.pages.flatMap((p) => p.items) ?? [];
  const filtered = q.status.length > 0 || q.review || q.q.trim() !== "";

  const update = (next: Partial<TenantListQuery>) =>
    setParams(paramsFromQuery({ ...q, ...next }), { replace: true });

  function toggleStatus(s: TenantStatus) {
    update({ status: q.status.includes(s) ? q.status.filter((x) => x !== s) : [...q.status, s] });
  }

  // a header click flips the order of the current sort, or starts a new one
  // in its natural order: names a to z, dates newest first
  function sortBy(s: TenantListQuery["sort"]) {
    if (s === q.sort) update({ order: q.order === "asc" ? "desc" : "asc" });
    else update({ sort: s, order: s === "name" ? "asc" : "desc" });
  }

  return (
    <section aria-labelledby="projects-h" className="flex flex-col gap-4">
      <h2 id="projects-h" className="text-section text-fg">
        Projects
      </h2>

      <div className="flex min-w-0 flex-col overflow-hidden rounded-lg border border-line bg-raised">
        <div className="flex flex-wrap items-center gap-x-4 gap-y-2.5 border-b border-line px-4 py-3">
          <SearchBox
            value={search}
            onChange={setSearch}
            onSubmit={() => update({ q: search })}
            placeholder="Name, address, website or site domain"
            className="w-full sm:w-80"
          />
          <fieldset className="flex flex-wrap items-center gap-1.5">
            <legend className="sr-only">Status</legend>
            {tenantStatus.options.map((s) => (
              <FilterChip key={s} on={q.status.includes(s)} onToggle={() => toggleStatus(s)}>
                {STATUS_TEXT[s]}
              </FilterChip>
            ))}
            <FilterChip on={q.review} onToggle={() => update({ review: !q.review })}>
              Needs review
            </FilterChip>
          </fieldset>
          <div className="flex flex-wrap items-center gap-x-4 gap-y-2 lg:ml-auto">
            <InlineSelect
              label="Sort by"
              value={q.sort}
              onChange={(v) => update({ sort: v as TenantListQuery["sort"] })}
            >
              {SORTS.map((s) => (
                <option key={s} value={s}>
                  {SORT_TEXT[s]}
                </option>
              ))}
            </InlineSelect>
            <InlineSelect
              label="Order"
              value={q.order}
              onChange={(v) => update({ order: v as TenantListQuery["order"] })}
            >
              <option value="desc">{q.sort === "name" ? "Z to A" : "Newest first"}</option>
              <option value="asc">{q.sort === "name" ? "A to Z" : "Oldest first"}</option>
            </InlineSelect>
          </div>
        </div>

        {list.isPending ? (
          <Loading label="Loading projects" className="px-4" rows={6} />
        ) : list.error ? (
          <ErrorPanel error={list.error} onRetry={() => list.refetch()} className="m-4" />
        ) : items.length === 0 ? (
          <StateBlock
            kind="empty"
            icon={<SearchX aria-hidden />}
            title="No project matches these filters"
            className="m-4"
            action={
              filtered ? (
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => update({ status: [], review: false, q: "" })}
                >
                  Clear filters
                </Button>
              ) : null
            }
          />
        ) : (
          <>
            <TableScroll label="Projects table" dim={list.isPlaceholderData}>
              <TenantTable items={items} sort={q.sort} order={q.order} onSort={sortBy} />
            </TableScroll>
            <div className="flex flex-wrap items-center justify-between gap-2 border-t border-line px-4 py-2.5">
              <p className="text-caption text-fg-3" aria-live="polite">
                {formatCount(items.length)} {items.length === 1 ? "project" : "projects"} shown
                {list.hasNextPage ? ", more to load" : ""}
              </p>
              {list.hasNextPage ? (
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => list.fetchNextPage()}
                  pending={list.isFetchingNextPage}
                >
                  {list.isFetchingNextPage ? "Loading" : "Load more"}
                </Button>
              ) : null}
            </div>
          </>
        )}
      </div>
    </section>
  );
}
