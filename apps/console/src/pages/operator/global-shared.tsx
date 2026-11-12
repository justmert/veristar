// controls shared by the global web view, the global api view and the
// comparison: day range, project filters (contract 8.1 and 8.2), sortable
// column headers, the selection for the comparison and the explanatory notes.

import { ArrowDown, ArrowUp, ArrowUpDown, Columns2, X } from "lucide-react";
import { type FormEvent, type ReactNode, useEffect, useState } from "react";
import { Link } from "react-router";
import { type TenantStatus, tenantStatus } from "@/api/schemas";
import { SelectField, TextField } from "@/components/form-field";
import { Button } from "@/components/ui/button";
import {
  activeFilterCount,
  compareSearch,
  type DayRange,
  EMPTY_FILTERS,
  formatDayRange,
  lastDays,
  MAX_COMPARE,
  type ProjectFilters,
  presetOf,
  RANGE_PRESETS,
  type Tri,
  type TriKey,
  validateDays,
} from "@/lib/global";

export const STATUS_TEXT: Record<TenantStatus, string> = {
  pending: "Pending",
  active: "Active",
  rejected: "Rejected",
  suspended: "Suspended",
  removed: "Removed",
};

// ---- day range

// presets of the last n utc days, and a custom pair of days. resolved is
// the range the server answered for, which is what the buttons reflect
export function RangeControl({
  resolved,
  onChange,
}: {
  resolved: DayRange | null;
  onChange: (r: DayRange) => void;
}) {
  const now = new Date();
  const active = resolved ? presetOf(resolved.from, resolved.to, now) : null;
  const [custom, setCustom] = useState(false);
  const [draft, setDraft] = useState<DayRange>(resolved ?? lastDays(30, now));
  const [errors, setErrors] = useState<{ from?: string; to?: string }>({});
  // the answer's range is a new object on every render; follow its days
  const rf = resolved?.from;
  const rt = resolved?.to;
  useEffect(() => {
    if (rf && rt) setDraft({ from: rf, to: rt });
  }, [rf, rt]);

  function submit(e: FormEvent) {
    e.preventDefault();
    const v = validateDays(draft, new Date());
    setErrors(v);
    if (v.from || v.to) return;
    onChange(draft);
  }

  return (
    <div className="space-y-3">
      <fieldset className="flex flex-wrap items-center gap-2">
        <legend className="float-left mr-2 text-sm font-medium">Range</legend>
        <div className="flex flex-wrap gap-1">
          {RANGE_PRESETS.map((n) => (
            <Button
              key={n}
              type="button"
              size="sm"
              variant={active === n && !custom ? "default" : "outline"}
              aria-pressed={active === n && !custom}
              onClick={() => {
                setCustom(false);
                onChange(lastDays(n, new Date()));
              }}
            >
              {n} days
            </Button>
          ))}
          <Button
            type="button"
            size="sm"
            variant={custom || (resolved !== null && active === null) ? "default" : "outline"}
            aria-pressed={custom || (resolved !== null && active === null)}
            onClick={() => setCustom(true)}
          >
            Custom
          </Button>
        </div>
      </fieldset>
      {custom ? (
        <form
          aria-label="Custom range"
          onSubmit={submit}
          noValidate
          className="flex flex-wrap items-end gap-3"
        >
          <div className="w-40">
            <TextField
              label="From (UTC day)"
              type="date"
              value={draft.from}
              error={errors.from}
              onChange={(e) => setDraft({ ...draft, from: e.target.value })}
            />
          </div>
          <div className="w-40">
            <TextField
              label="To (UTC day)"
              type="date"
              value={draft.to}
              error={errors.to}
              onChange={(e) => setDraft({ ...draft, to: e.target.value })}
            />
          </div>
          <Button type="submit" variant="outline">
            Apply range
          </Button>
        </form>
      ) : null}
      {resolved ? (
        <p className="text-sm text-muted-foreground" aria-live="polite">
          {formatDayRange(resolved.from, resolved.to)}. Today counts what the hourly aggregation has
          read so far.
        </p>
      ) : null}
    </div>
  );
}

// ---- project filters

const TRI_TEXT: Record<TriKey, { label: string; yes: string; no: string }> = {
  verified: { label: "Domain verified", yes: "At least one site", no: "No site" },
  stellar_toml: { label: "stellar.toml", yes: "Found on a site", no: "Not found" },
  has_web_data: { label: "Web data", yes: "Has page views", no: "No page views" },
  has_api_data: { label: "API data", yes: "Has requests", no: "No requests" },
};

export function FiltersPanel({
  filters,
  onChange,
}: {
  filters: ProjectFilters;
  onChange: (f: ProjectFilters) => void;
}) {
  const [search, setSearch] = useState(filters.q);
  useEffect(() => setSearch(filters.q), [filters.q]);
  const n = activeFilterCount(filters);

  function toggle(s: TenantStatus) {
    onChange({
      ...filters,
      statuses: filters.statuses.includes(s)
        ? filters.statuses.filter((x) => x !== s)
        : [...filters.statuses, s],
    });
  }

  return (
    <div className="space-y-4">
      <form
        aria-label="Search projects"
        onSubmit={(e) => {
          e.preventDefault();
          onChange({ ...filters, q: search });
        }}
        className="flex flex-wrap items-end gap-2"
      >
        <div className="min-w-0 flex-1 basis-56">
          <TextField
            label="Search"
            type="search"
            maxLength={100}
            placeholder="Project name or slug"
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
              checked={filters.statuses.includes(s)}
              onChange={() => toggle(s)}
            />
            {STATUS_TEXT[s]}
          </label>
        ))}
      </fieldset>

      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        {(Object.keys(TRI_TEXT) as TriKey[]).map((k) => (
          <SelectField
            key={k}
            label={TRI_TEXT[k].label}
            value={filters[k]}
            onChange={(e) => onChange({ ...filters, [k]: e.target.value as Tri })}
          >
            <option value="">Any</option>
            <option value="true">{TRI_TEXT[k].yes}</option>
            <option value="false">{TRI_TEXT[k].no}</option>
          </SelectField>
        ))}
      </div>

      <div className="flex flex-wrap items-end gap-3">
        <div className="w-40">
          <TextField
            label="Enrolled from"
            type="date"
            value={filters.enrolled_from}
            onChange={(e) => onChange({ ...filters, enrolled_from: e.target.value })}
          />
        </div>
        <div className="w-40">
          <TextField
            label="Enrolled to"
            type="date"
            value={filters.enrolled_to}
            onChange={(e) => onChange({ ...filters, enrolled_to: e.target.value })}
          />
        </div>
        {n > 0 ? (
          <Button type="button" variant="ghost" onClick={() => onChange(EMPTY_FILTERS)}>
            <X aria-hidden />
            Clear filters ({n})
          </Button>
        ) : null}
      </div>
    </div>
  );
}

// ---- sortable headers

export function SortHeader<S extends string>({
  label,
  sort,
  current,
  order,
  numeric,
  onSort,
}: {
  label: string;
  sort: S;
  current: S;
  order: "asc" | "desc";
  numeric?: boolean;
  onSort: (s: S) => void;
}) {
  const on = current === sort;
  return (
    <th
      scope="col"
      aria-sort={on ? (order === "asc" ? "ascending" : "descending") : "none"}
      className={`px-2 py-1 font-medium whitespace-nowrap ${numeric ? "text-right" : ""}`}
    >
      <button
        type="button"
        onClick={() => onSort(sort)}
        className={`inline-flex items-center gap-1 rounded px-1 py-1 hover:text-foreground ${on ? "text-foreground" : ""}`}
      >
        {label}
        {on ? (
          order === "asc" ? (
            <ArrowUp aria-hidden className="size-3.5" />
          ) : (
            <ArrowDown aria-hidden className="size-3.5" />
          )
        ) : (
          <ArrowUpDown aria-hidden className="size-3.5 opacity-50" />
        )}
      </button>
    </th>
  );
}

export function Missing() {
  return (
    <>
      <span aria-hidden>-</span>
      <span className="sr-only">none</span>
    </>
  );
}

export function Num({ children }: { children: ReactNode }) {
  return <td className="tabular px-3 py-2 text-right whitespace-nowrap">{children}</td>;
}

export function orMissing(v: string | null): ReactNode {
  return v === null ? <Missing /> : v;
}

// ---- selection for the comparison

export type Selection = { id: string; name: string }[];

export function useSelection() {
  const [sel, setSel] = useState<Selection>([]);
  const has = (id: string) => sel.some((s) => s.id === id);
  const toggle = (id: string, name: string) =>
    setSel((cur) =>
      cur.some((s) => s.id === id)
        ? cur.filter((s) => s.id !== id)
        : cur.length >= MAX_COMPARE
          ? cur
          : [...cur, { id, name }],
    );
  return { sel, has, toggle, clear: () => setSel([]), full: sel.length >= MAX_COMPARE };
}

export function SelectCell({
  id,
  name,
  selection,
}: {
  id: string;
  name: string;
  selection: ReturnType<typeof useSelection>;
}) {
  const checked = selection.has(id);
  return (
    <td className="py-2 pr-1 pl-4 sm:pl-5">
      <input
        type="checkbox"
        className="size-4 accent-primary"
        aria-label={`Compare ${name}`}
        checked={checked}
        disabled={!checked && selection.full}
        onChange={() => selection.toggle(id, name)}
      />
    </td>
  );
}

export function CompareBar({
  selection,
  range,
}: {
  selection: ReturnType<typeof useSelection>;
  range: DayRange | null;
}) {
  const { sel } = selection;
  return (
    <section
      aria-label="Comparison"
      className="flex flex-wrap items-center gap-2 rounded-xl border bg-card p-3 text-sm sm:px-5"
    >
      <p className="text-muted-foreground">
        {sel.length === 0
          ? `Select up to ${MAX_COMPARE} projects to compare them side by side.`
          : `${sel.length} of ${MAX_COMPARE} selected:`}
      </p>
      {sel.length > 0 ? (
        <ul aria-label="Selected projects" className="flex flex-wrap gap-1.5">
          {sel.map((s) => (
            <li key={s.id}>
              <button
                type="button"
                onClick={() => selection.toggle(s.id, s.name)}
                aria-label={`Remove ${s.name} from the comparison`}
                className="inline-flex max-w-56 items-center gap-1 rounded-full border bg-muted px-2.5 py-0.5"
              >
                <span className="truncate">{s.name}</span>
                <X aria-hidden className="size-3.5 shrink-0" />
              </button>
            </li>
          ))}
        </ul>
      ) : null}
      <div className="ml-auto flex gap-2">
        {sel.length > 0 ? (
          <Button type="button" variant="ghost" size="sm" onClick={selection.clear}>
            Clear
          </Button>
        ) : null}
        {sel.length > 0 ? (
          <Button asChild size="sm">
            <Link
              to={`/operator/compare?${compareSearch(
                sel.map((s) => s.id),
                range ?? { from: "", to: "" },
              )}`}
            >
              <Columns2 aria-hidden />
              Compare
            </Link>
          </Button>
        ) : (
          <Button size="sm" disabled>
            <Columns2 aria-hidden />
            Compare
          </Button>
        )}
      </div>
    </section>
  );
}

// ---- explanatory notes

export const VISITORS_NOTE =
  "Visitors are unique per UTC day and site. A person who visits on Tuesday and Wednesday counts twice, so visitors over a range are the sum of the daily visitors, and one person on two sites of a project counts once per site.";
export const QUARANTINE_NOTE =
  "Quarantined traffic came from an address range that sent more distinct visitors to a site within an hour than the limit allows (shared networks such as carrier NAT, VPNs and offices also trip it). It is kept in a separate band and never mixed into the numbers above; the share is quarantined page views over all page views.";
export const REJECTED_NOTE =
  "Rejected requests were refused by the gateway (invalid proof, wrong origin, rate limits, and browsers asking not to be tracked with Global Privacy Control); the share is rejected over all requests the gateway answered.";
export const AGGREGATE_NOTE =
  "Every number here comes from daily aggregates. Web numbers are copied from Umami once an hour; API numbers from the daily rollup, refreshed every 5 minutes.";

export function Notes({ items, children }: { items: string[]; children?: ReactNode }) {
  return (
    <aside
      aria-label="About these numbers"
      className="rounded-xl border bg-card p-4 text-sm sm:p-5"
    >
      <h3 className="font-semibold">About these numbers</h3>
      <ul className="mt-2 list-disc space-y-1.5 pl-5 text-muted-foreground">
        {items.map((t) => (
          <li key={t}>{t}</li>
        ))}
      </ul>
      {children}
    </aside>
  );
}
