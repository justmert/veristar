import { useInfiniteQuery } from "@tanstack/react-query";
import { type FormEvent, useState } from "react";
import { useSearchParams } from "react-router";
import type { TenantListQuery } from "@/api/endpoints";
import { operatorTenantsQuery } from "@/api/queries";
import { type TenantStatus, tenantStatus } from "@/api/schemas";
import { ErrorPanel, Loading } from "@/components/error-panel";
import { SelectField, TextField } from "@/components/form-field";
import { Button } from "@/components/ui/button";
import { TenantList } from "./tenant-table";

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
  const list = useInfiniteQuery(operatorTenantsQuery(q));

  const update = (next: Partial<TenantListQuery>) =>
    setParams(paramsFromQuery({ ...q, ...next }), { replace: true });

  function submitSearch(e: FormEvent) {
    e.preventDefault();
    update({ q: search });
  }

  function toggleStatus(s: TenantStatus) {
    update({ status: q.status.includes(s) ? q.status.filter((x) => x !== s) : [...q.status, s] });
  }

  return (
    <section className="space-y-5">
      <h2 className="text-xl font-semibold tracking-tight">Projects</h2>

      <div className="space-y-4 rounded-xl border bg-card p-4 sm:p-5">
        <form
          aria-label="Search projects"
          onSubmit={submitSearch}
          className="flex flex-wrap items-end gap-2"
        >
          <div className="min-w-60 flex-1">
            <TextField
              label="Search"
              type="search"
              name="q"
              maxLength={100}
              placeholder="Name, address, website or site domain"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </div>
          <Button type="submit" variant="outline">
            Search
          </Button>
        </form>

        <fieldset className="flex flex-wrap items-center gap-x-4 gap-y-2 text-sm">
          <legend className="mb-1 font-medium">Status</legend>
          {tenantStatus.options.map((s) => (
            <label key={s} className="flex items-center gap-1.5">
              <input
                type="checkbox"
                className="size-4 accent-primary"
                checked={q.status.includes(s)}
                onChange={() => toggleStatus(s)}
              />
              {STATUS_TEXT[s]}
            </label>
          ))}
          <label className="flex items-center gap-1.5">
            <input
              type="checkbox"
              className="size-4 accent-primary"
              checked={q.review}
              onChange={() => update({ review: !q.review })}
            />
            Needs review
          </label>
        </fieldset>

        <div className="flex flex-wrap gap-4 text-sm">
          <SelectField
            label="Sort by"
            value={q.sort}
            onChange={(e) => update({ sort: e.target.value as TenantListQuery["sort"] })}
          >
            {SORTS.map((s) => (
              <option key={s} value={s}>
                {SORT_TEXT[s]}
              </option>
            ))}
          </SelectField>
          <SelectField
            label="Order"
            value={q.order}
            onChange={(e) => update({ order: e.target.value as TenantListQuery["order"] })}
          >
            <option value="desc">{q.sort === "name" ? "Z to A" : "Newest first"}</option>
            <option value="asc">{q.sort === "name" ? "A to Z" : "Oldest first"}</option>
          </SelectField>
        </div>
      </div>

      {list.isPending ? (
        <Loading label="Loading projects" />
      ) : list.error ? (
        <ErrorPanel error={list.error} onRetry={() => list.refetch()} />
      ) : (
        <>
          {list.data.pages[0]?.items.length === 0 ? (
            <p className="rounded-xl border border-dashed bg-card p-6 text-muted-foreground">
              No project matches these filters.
            </p>
          ) : (
            <TenantList items={list.data.pages.flatMap((p) => p.items)} />
          )}
          {list.hasNextPage ? (
            <Button
              variant="outline"
              onClick={() => list.fetchNextPage()}
              disabled={list.isFetchingNextPage}
            >
              {list.isFetchingNextPage ? "Loading" : "Load more"}
            </Button>
          ) : null}
        </>
      )}
    </section>
  );
}
