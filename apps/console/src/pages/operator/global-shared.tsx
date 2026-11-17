// controls shared by the operator project tables (the project list, global
// web, global api) and the comparison: day range, project filters (contract
// 8.1 and 8.2), sortable column headers with a magnitude bar on the sort
// column, the selection for the comparison and the explanatory notes.

import { useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { ArrowDown, ArrowUp, ArrowUpDown, Columns2, Search, X } from "lucide-react";
import { type FormEvent, type ReactNode, useEffect, useId, useState } from "react";
import { Link, useLocation } from "react-router";
import { type Cohort, type CohortValue, cohortListQuery, type Kpi } from "@/api/cohorts";
import { type TenantStatus, tenantStatus } from "@/api/schemas";
import { SERIES_BG } from "@/components/compare-charts";
import { NativeSelect, TextField } from "@/components/form-field";
import { Segmented } from "@/components/segmented";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import {
  activeFilterCount,
  COHORT_KEYS,
  type CohortKey,
  compareSearch,
  type DayRange,
  dayPairError,
  EMPTY_FILTERS,
  formatChange,
  formatDayRange,
  formatPercentile,
  type IntegrityFilter,
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

type RangeChoice = `${(typeof RANGE_PRESETS)[number]}` | "custom";

// presets of the last n utc days, and a custom pair of days. resolved is
// the range the server answered for, which is what the control reflects
export function RangeControl({
  resolved,
  onChange,
  className,
}: {
  resolved: DayRange | null;
  onChange: (r: DayRange) => void;
  className?: string;
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

  const value: RangeChoice =
    custom || (resolved !== null && active === null)
      ? "custom"
      : (`${active ?? 30}` as RangeChoice);

  function submit(e: FormEvent) {
    e.preventDefault();
    const v = validateDays(draft, new Date());
    setErrors(v);
    if (v.from || v.to) return;
    onChange(draft);
  }

  return (
    <div className={cn("flex flex-col gap-2", className)}>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <Segmented<RangeChoice>
          label="Range"
          value={value}
          options={[
            ...RANGE_PRESETS.map((n) => ({ value: `${n}` as RangeChoice, label: `${n} days` })),
            { value: "custom", label: "Custom" },
          ]}
          onChange={(v) => {
            if (v === "custom") {
              setCustom(true);
              return;
            }
            setCustom(false);
            onChange(lastDays(Number(v), new Date()));
          }}
        />
        {resolved ? (
          <span className="text-caption text-fg-3" aria-live="polite">
            {formatDayRange(resolved.from, resolved.to)}. Today counts what the hourly aggregation
            has read so far.
          </span>
        ) : null}
      </div>
      {custom ? (
        <form
          aria-label="Custom range"
          onSubmit={submit}
          noValidate
          className="flex animate-reveal-in flex-wrap items-end gap-3"
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
    </div>
  );
}

// ---- toolbar pieces

// a search box with its submit button; the label is read, not shown, since
// the icon and placeholder say what it is
export function SearchBox({
  value,
  onChange,
  onSubmit,
  placeholder,
  className,
}: {
  value: string;
  onChange: (v: string) => void;
  onSubmit: () => void;
  placeholder: string;
  className?: string;
}) {
  const id = useId();
  return (
    <form
      aria-label="Search projects"
      onSubmit={(e) => {
        e.preventDefault();
        onSubmit();
      }}
      className={cn("flex min-w-0 items-center gap-1.5", className)}
    >
      <label htmlFor={id} className="sr-only">
        Search
      </label>
      <div className="relative min-w-0 flex-1">
        <Search
          aria-hidden
          className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-fg-3"
        />
        <Input
          id={id}
          type="search"
          name="q"
          maxLength={100}
          placeholder={placeholder}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          className="h-(--vs-control-sm) pl-8 max-lg:h-(--vs-control-touch)"
        />
      </div>
      <Button type="submit" variant="outline" size="sm" className="max-lg:h-(--vs-control-touch)">
        Search
      </Button>
    </form>
  );
}

// a filter that is on or off, drawn as a chip; several can be on
export function FilterChip({
  on,
  onToggle,
  children,
}: {
  on: boolean;
  onToggle: () => void;
  children: ReactNode;
}) {
  return (
    <button
      type="button"
      aria-pressed={on}
      onClick={onToggle}
      className={cn(
        "inline-flex h-7 shrink-0 items-center gap-1.5 rounded-full border px-2.5 text-caption font-medium transition-colors duration-(--vs-duration-press) ease-standard focus-ring max-lg:h-9",
        on
          ? "border-accent-text bg-selected text-fg"
          : "border-line-strong text-fg-2 hover:bg-hover hover:text-fg",
      )}
    >
      {children}
    </button>
  );
}

// a native select with its label in front, for a toolbar row
export function InlineSelect({
  label,
  value,
  onChange,
  children,
  disabled,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  children: ReactNode;
  disabled?: boolean;
}) {
  const id = useId();
  return (
    <span className="flex min-w-0 gap-1.5 max-sm:flex-col sm:items-center">
      <label htmlFor={id} className="text-caption whitespace-nowrap text-fg-3">
        {label}
      </label>
      <NativeSelect
        id={id}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        size="sm"
        disabled={disabled}
      >
        {children}
      </NativeSelect>
    </span>
  );
}

// ---- project filters

const TRI_TEXT: Record<TriKey, { label: string; yes: string; no: string }> = {
  verified: { label: "Domain verified", yes: "At least one site", no: "No site" },
  stellar_toml: { label: "stellar.toml", yes: "Found on a site", no: "Not found" },
  has_web_data: { label: "Web data", yes: "Has page views", no: "No page views" },
  has_api_data: { label: "API data", yes: "Has requests", no: "No requests" },
};

// every filter of contract 8.2 in one toolbar: search, the status chips, the
// evidence and data selects and the enrollment days
export function FiltersPanel({
  filters,
  onChange,
  integrity,
}: {
  filters: ProjectFilters;
  onChange: (f: ProjectFilters) => void;
  // the web list filters on the integrity grade; the api list has none
  integrity?: boolean;
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
    <div className="flex flex-col gap-2.5">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <SearchBox
          value={search}
          onChange={setSearch}
          onSubmit={() => onChange({ ...filters, q: search })}
          placeholder="Project name or slug"
          className="w-full sm:w-80"
        />
        <fieldset className="flex flex-wrap items-center gap-1.5">
          <legend className="sr-only">Status</legend>
          {tenantStatus.options.map((s) => (
            <FilterChip key={s} on={filters.statuses.includes(s)} onToggle={() => toggle(s)}>
              {STATUS_TEXT[s]}
            </FilterChip>
          ))}
        </fieldset>
      </div>
      <div className="grid grid-cols-2 gap-x-3 gap-y-2 sm:flex sm:flex-wrap sm:items-center sm:gap-x-4">
        {(Object.keys(TRI_TEXT) as TriKey[]).map((k) => (
          <InlineSelect
            key={k}
            label={TRI_TEXT[k].label}
            value={filters[k]}
            onChange={(v) => onChange({ ...filters, [k]: v as Tri })}
          >
            <option value="">Any</option>
            <option value="true">{TRI_TEXT[k].yes}</option>
            <option value="false">{TRI_TEXT[k].no}</option>
          </InlineSelect>
        ))}
        {integrity ? (
          <InlineSelect
            label="Integrity"
            value={filters.integrity}
            onChange={(v) => onChange({ ...filters, integrity: v as IntegrityFilter })}
          >
            <option value="">Any grade</option>
            <option value="C">C</option>
            <option value="B">B and below</option>
            <option value="A">Graded</option>
          </InlineSelect>
        ) : null}
        <DayInput
          label="Enrolled from"
          value={filters.enrolled_from}
          onChange={(v) => onChange({ ...filters, enrolled_from: v })}
        />
        <DayInput
          label="Enrolled to"
          value={filters.enrolled_to}
          onChange={(v) => onChange({ ...filters, enrolled_to: v })}
        />
      </div>
      <CohortFilters filters={filters} onChange={onChange} />
      {dayPairError(filters) ? (
        <p role="alert" className="text-small text-critical">
          {dayPairError(filters)}
        </p>
      ) : null}
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2 empty:hidden">
        {n > 0 ? (
          <Button type="button" variant="ghost" size="sm" onClick={() => onChange(EMPTY_FILTERS)}>
            <X aria-hidden />
            Clear filters ({n})
          </Button>
        ) : null}
      </div>
    </div>
  );
}

const COHORT_TEXT: Record<CohortKey, string> = {
  program: "Program",
  round: "Round",
  category: "Category",
};

// the cohort filters of contract 19.1: the values operators confirmed, read
// from the cohort list. they narrow the list and set the population every
// percentile column is computed in
function CohortFilters({
  filters,
  onChange,
}: {
  filters: ProjectFilters;
  onChange: (f: ProjectFilters) => void;
}) {
  const list = useQuery(cohortListQuery);
  const values: Record<CohortKey, CohortValue[]> = {
    program: list.data?.programs ?? [],
    round: list.data?.rounds ?? [],
    category: list.data?.categories ?? [],
  };
  return (
    <fieldset
      className="grid grid-cols-2 gap-x-3 gap-y-2 sm:flex sm:flex-wrap sm:items-center sm:gap-x-4"
      aria-busy={list.isPending || undefined}
    >
      <legend className="sr-only">SCF cohort</legend>
      {list.error ? (
        <span className="col-span-2 flex items-center gap-2 text-caption text-fg-2" role="alert">
          The cohort values could not be loaded.
          <Button type="button" variant="ghost" size="xs" onClick={() => list.refetch()}>
            Try again
          </Button>
        </span>
      ) : null}
      {COHORT_KEYS.map((k) => {
        const opts = values[k].map((v) => v.value);
        // a value from the address that no project holds any more stays
        // selectable, so the filter shown is the filter sent
        if (filters[k] && !opts.includes(filters[k])) opts.unshift(filters[k]);
        return (
          <InlineSelect
            key={k}
            label={COHORT_TEXT[k]}
            value={filters[k]}
            onChange={(v) => onChange({ ...filters, [k]: v })}
            disabled={list.isPending}
          >
            <option value="">{list.isPending ? "Loading" : "Any"}</option>
            {opts.map((o) => (
              <option key={o} value={o}>
                {o}
              </option>
            ))}
          </InlineSelect>
        );
      })}
      <DayInput
        label="Awarded from"
        value={filters.award_from}
        onChange={(v) => onChange({ ...filters, award_from: v })}
      />
      <DayInput
        label="Awarded to"
        value={filters.award_to}
        onChange={(v) => onChange({ ...filters, award_to: v })}
      />
    </fieldset>
  );
}

// ---- cohort and key metric cells (contract 19.2)

// the sort names of the change and percentile columns, shared by both lists
export const GROWTH_SORT_TEXT = {
  visitors_growth: "Visitors change",
  pageviews_growth: "Page views change",
  requests_growth: "Requests change",
  error_rate_change: "Error rate change",
  onchain_operations_growth: "On-chain operations change",
  visitors_percentile: "Visitors percentile",
  pageviews_percentile: "Page views percentile",
  requests_percentile: "Requests percentile",
  error_rate_percentile: "Error rate percentile",
  onchain_operations_percentile: "On-chain operations percentile",
} as const;

export function CohortLine({ cohort }: { cohort: Cohort }) {
  const text = cohortText(cohort);
  if (!text) return null;
  return (
    <span data-cohort className="mt-0.5 block truncate text-caption text-fg-3">
      {text}
      {cohort.award_date ? `, awarded ${cohort.award_date}` : ""}
    </span>
  );
}

// the cohort a project is confirmed in, as one short line
export function cohortText(c: Cohort): string | null {
  const parts = [c.program, c.round, c.category].filter((x): x is string => !!x);
  return parts.length ? parts.join(", ") : null;
}

// a key metric's change against the previous period of equal length, with
// its percentile in the cohort beneath. the title says what both mean.
export function KpiCell({
  k,
  kind,
  on,
  label,
}: {
  k: Kpi;
  kind: "relative" | "points";
  on: boolean;
  label: string;
}) {
  const change = formatChange(k.change, kind);
  const pct = formatPercentile(k.percentile);
  const title = [
    change === null
      ? `${label}: no previous value to compare with`
      : `${label}: ${change} against the previous period`,
    pct === null ? null : `${pct}: at or above ${pct.slice(1)}% of the cohort`,
  ]
    .filter(Boolean)
    .join(". ");
  return (
    <Num className={on ? "text-fg" : undefined}>
      <span className="flex flex-col items-end leading-tight" title={title}>
        <span data-kpi="change">{orMissing(change)}</span>
        {pct ? (
          <span data-kpi="percentile" className="text-caption text-fg-3">
            {pct} in cohort
          </span>
        ) : null}
      </span>
    </Num>
  );
}

function DayInput({
  label,
  value,
  onChange,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
}) {
  const id = useId();
  return (
    <span className="flex min-w-0 gap-1.5 max-sm:flex-col sm:items-center">
      <label htmlFor={id} className="text-caption whitespace-nowrap text-fg-3">
        {label}
      </label>
      <Input
        id={id}
        type="date"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="h-(--vs-control-sm) sm:w-38 max-lg:h-(--vs-control-touch)"
      />
    </span>
  );
}

// ---- table pieces

// the scroll box of a project table: the header sticks while the rows scroll,
// and it scrolls sideways on a phone inside its panel
export function TableScroll({
  label,
  dim,
  children,
}: {
  label: string;
  dim?: boolean;
  children: ReactNode;
}) {
  return (
    <section
      aria-label={label}
      // biome-ignore lint/a11y/noNoninteractiveTabindex: a scrollable region needs focus (wcag 2.1.1)
      tabIndex={0}
      className={cn(
        "relative max-h-[70dvh] overflow-auto transition-opacity duration-(--vs-duration-change) ease-standard focus-ring",
        dim && "opacity-60",
      )}
    >
      {children}
    </section>
  );
}

export const TH =
  "sticky top-0 z-10 h-9 border-b border-line bg-raised px-3 text-left align-middle text-caption font-medium whitespace-nowrap text-fg-3";
export const TD = "h-11 border-b border-line-subtle px-3 align-middle";
export const ROW =
  "transition-colors duration-(--vs-duration-press) ease-standard hover:bg-hover data-[state=selected]:bg-selected";

export function SortHeader<S extends string>({
  label,
  sort,
  current,
  order,
  numeric,
  onSort,
  className,
}: {
  label: string;
  sort: S;
  current: S;
  order: "asc" | "desc";
  numeric?: boolean;
  onSort: (s: S) => void;
  className?: string;
}) {
  const on = current === sort;
  return (
    <th
      scope="col"
      aria-sort={on ? (order === "asc" ? "ascending" : "descending") : "none"}
      className={cn(TH, numeric && "text-right", on && "text-fg", className)}
    >
      <button
        type="button"
        onClick={() => onSort(sort)}
        className={cn(
          "-mx-1 inline-flex items-center gap-1 rounded-sm px-1 py-1 transition-colors duration-(--vs-duration-press) ease-standard hover:text-fg focus-ring",
          on ? "text-fg" : "",
        )}
      >
        {label}
        {on ? (
          order === "asc" ? (
            <ArrowUp aria-hidden className="size-3" />
          ) : (
            <ArrowDown aria-hidden className="size-3" />
          )
        ) : (
          <ArrowUpDown aria-hidden className="size-3 text-fg-disabled" />
        )}
      </button>
    </th>
  );
}

// a number with a bar of its size against the largest loaded value, drawn
// only on the column the table is sorted by, so the long tail shows at once
// a number with the count behind it on a second line: shown, not left to a
// hover title, so keyboard and screen reader users read it too
export function WithDetail({ children, detail }: { children: ReactNode; detail: ReactNode }) {
  return (
    <span className="inline-flex flex-col items-end">
      <span>{children}</span>
      <span className="font-sans text-caption text-fg-3">{detail}</span>
    </span>
  );
}

export function MagnitudeCell({
  children,
  value,
  max,
  on,
}: {
  children: ReactNode;
  value: number | null;
  max: number;
  on: boolean;
}) {
  if (!on) return <Num>{children}</Num>;
  const w = value === null || max <= 0 ? 0 : Math.max(1, (value / max) * 100);
  return (
    <td className={cn(TD, "font-mono text-small text-fg tabular")}>
      <span className="flex items-center justify-end gap-2.5">
        <span className="h-1.5 w-20 overflow-hidden rounded-full bg-line-subtle max-md:hidden">
          <span
            className="block h-full rounded-full bg-series-1 transition-[width] duration-(--vs-duration-change) ease-standard"
            // the width is data; react writes it through the cssom
            style={{ width: `${w}%` }}
          />
        </span>
        <span className="whitespace-nowrap">{children}</span>
      </span>
    </td>
  );
}

export function Missing() {
  return (
    <>
      <span aria-hidden className="text-fg-3">
        -
      </span>
      <span className="sr-only">none</span>
    </>
  );
}

export function Num({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <td
      className={cn(
        TD,
        "text-right font-mono text-small whitespace-nowrap text-fg-2 tabular",
        className,
      )}
    >
      {children}
    </td>
  );
}

export function orMissing(v: string | null): ReactNode {
  return v === null ? <Missing /> : v;
}

// ---- selection for the comparison

export type Selection = { id: string; name: string }[];

// the comparison page hands its projects over in the router state when an
// operator goes to add one, so the table starts with them selected
export type CompareHandover = { compare: Selection };

function handedOver(state: unknown): Selection {
  const c = (state as Partial<CompareHandover> | null)?.compare;
  if (!Array.isArray(c)) return [];
  return c
    .filter(
      (s): s is { id: string; name: string } =>
        typeof s?.id === "string" && typeof s?.name === "string",
    )
    .slice(0, MAX_COMPARE);
}

export function useSelection() {
  const location = useLocation();
  const [sel, setSel] = useState<Selection>(() => handedOver(location.state));
  const has = (id: string) => sel.some((s) => s.id === id);
  const slot = (id: string) => sel.findIndex((s) => s.id === id);
  const toggle = (id: string, name: string) =>
    setSel((cur) =>
      cur.some((s) => s.id === id)
        ? cur.filter((s) => s.id !== id)
        : cur.length >= MAX_COMPARE
          ? cur
          : [...cur, { id, name }],
    );
  return { sel, has, slot, toggle, clear: () => setSel([]), full: sel.length >= MAX_COMPARE };
}

// a selected project takes the series colour it will have in the comparison
export function SeriesSwatch({ slot, className }: { slot: number; className?: string }) {
  if (slot < 0) return null;
  return (
    <span
      aria-hidden
      className={cn("inline-block size-2 shrink-0 rounded-bar", SERIES_BG[slot], className)}
    />
  );
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
    <td className={cn(TD, "w-10 pr-0 pl-4")}>
      <Checkbox
        aria-label={`Compare ${name}`}
        checked={checked}
        disabled={!checked && selection.full}
        onCheckedChange={() => selection.toggle(id, name)}
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
      className={cn(
        "flex flex-wrap items-center gap-2 border-b border-line px-4 py-2 text-small transition-colors duration-(--vs-duration-change) ease-standard",
        sel.length > 0 ? "bg-overlay" : "",
      )}
    >
      <p className="text-fg-2">
        {sel.length === 0 ? (
          `Select up to ${MAX_COMPARE} projects to compare them side by side.`
        ) : (
          <>
            <span className="font-mono text-fg">{sel.length}</span> of {MAX_COMPARE} selected:
          </>
        )}
      </p>
      {sel.length > 0 ? (
        <ul aria-label="Selected projects" className="flex flex-wrap gap-1.5">
          {sel.map((s, i) => (
            <li key={s.id} className="animate-reveal-in">
              <button
                type="button"
                onClick={() => selection.toggle(s.id, s.name)}
                aria-label={`Remove ${s.name} from the comparison`}
                className="inline-flex h-7 max-w-56 items-center gap-1.5 rounded-full border border-line-strong pr-1.5 pl-2 text-caption text-fg transition-colors duration-(--vs-duration-press) ease-standard hover:bg-hover focus-ring"
              >
                <SeriesSwatch slot={i} />
                <span className="truncate">{s.name}</span>
                <X aria-hidden className="size-3.5 shrink-0 text-fg-3" />
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
          <Button asChild size="sm" className="max-lg:h-(--vs-control-touch)">
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
export const COVERAGE_NOTE =
  "Not measured is the estimated share of visits from browsers with Global Privacy Control (GPC), which load the tracker but are never measured. It is the share of tracker loads that carried the signal; below 100 loads the column shows a dash and no estimate.";
export const AGGREGATE_NOTE =
  "Every number here comes from daily aggregates. Web numbers are copied from Umami once an hour; API numbers from the daily rollup, refreshed every 5 minutes.";

export const COHORT_NOTE =
  "Change columns compare the range with the previous range of equal length. A percentile is the share of the cohort's projects with a value at or below this one, among active and suspended projects that match the cohort filters (all projects without them); for the error rate a higher percentile means more errors.";

export function Notes({ items, children }: { items: string[]; children?: ReactNode }) {
  return (
    <aside aria-label="About these numbers" className="flex flex-col gap-2 text-caption">
      <h3 className="text-small font-medium text-fg-2">About these numbers</h3>
      <ul className="flex max-w-prose flex-col gap-1.5 text-fg-3">
        {items.map((t) => (
          <li key={t}>{t}</li>
        ))}
      </ul>
      {children}
    </aside>
  );
}
