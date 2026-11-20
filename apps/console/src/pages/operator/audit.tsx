import { useInfiniteQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { Bot, CalendarDays, ChevronDown, Cog, User } from "lucide-react";
import { useEffect, useId, useState } from "react";
import { Link, useSearchParams } from "react-router";
import type { AuditQuery } from "@/api/endpoints";
import { auditQuery } from "@/api/queries";
import type { AuditEntry } from "@/api/schemas";
import { Avatar } from "@/components/avatar";
import { type Filter, FilterBar } from "@/components/filter-bar";
import { SelectField, TextField } from "@/components/form-field";
import { PageHeader } from "@/components/page-header";
import { DateField, isIsoDay } from "@/components/range-control";
import { EmptyState, ErrorState, LoadingState, Skeleton } from "@/components/states";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import {
  ACTION_FAMILIES,
  type AuditItem,
  auditChanges,
  auditDaysError,
  auditReason,
  auditSentence,
  dayToUtc,
  foldRuns,
  utcToDay,
} from "@/lib/audit";
import { countOf, formatDate, formatDateTimeUtc, formatTimeUtc } from "@/lib/format";

const FIELDS = ["q", "tenant_id", "action", "actor", "target", "from", "to"] as const;
type Field = (typeof FIELDS)[number];

export function auditFromParams(p: URLSearchParams): AuditQuery {
  return {
    tenant_id: p.get("tenant_id") ?? "",
    action: p.get("action") ?? "",
    actor: p.get("actor") ?? "",
    target: p.get("target") ?? "",
    q: p.get("q") ?? "",
    from: p.get("from") ?? "",
    to: p.get("to") ?? "",
  };
}

const TEXT_DELAY_MS = 400;

// who did what, newest first. every filter applies as it changes; the days
// are utc days. the log reads as sentences, with the codes as details.
export function AuditPage() {
  const [params, setParams] = useSearchParams();
  const q = auditFromParams(params);
  const from = utcToDay(q.from, false);
  const to = utcToDay(q.to, true);
  const daysError = auditDaysError(from, to);
  const list = useInfiniteQuery({ ...auditQuery(q), enabled: daysError === null });
  const entries = list.data?.pages.flatMap((p) => p.items) ?? [];

  function setOne(f: Field, value: string) {
    const next = new URLSearchParams(params);
    if (value) next.set(f, value);
    else next.delete(f);
    setParams(next, { replace: true });
  }
  const clear = () => setParams(new URLSearchParams(), { replace: true });
  const filtered = FIELDS.some((f) => (q[f] ?? "") !== "");

  // the server searches the whole log (5.9) as the person types, after a
  // short pause, so each key press does not ask it
  const currentQ = q.q ?? "";
  const [search, setSearch] = useState(currentQ);
  useEffect(() => setSearch(currentQ), [currentQ]);
  useEffect(() => {
    const t = search.trim();
    if (t === currentQ) return;
    const timer = setTimeout(() => setOne("q", t), TEXT_DELAY_MS);
    return () => clearTimeout(timer);
  });

  const family = ACTION_FAMILIES.find((a) => a.value === q.action);
  const filters: Filter[] = [
    {
      key: "action",
      label: "Action",
      control: (
        <SelectField
          label="Action"
          value={q.action}
          onChange={(e) => setOne("action", e.target.value)}
        >
          <option value="">All actions</option>
          {q.action && !family ? <option value={q.action}>{q.action}</option> : null}
          {ACTION_FAMILIES.map((a) => (
            <option key={a.value} value={a.value}>
              {a.label}
            </option>
          ))}
        </SelectField>
      ),
      active: q.action
        ? { label: family?.label ?? q.action, onClear: () => setOne("action", "") }
        : null,
    },
    textFilter("tenant_id", "Project id", "The id from the address of a project page.", q, setOne),
    textFilter("actor", "Actor", "As user:<id>, or system:<job> for the system.", q, setOne),
    textFilter("target", "Target", "As tenant:<id>, site:<id> or rule:<id>.", q, setOne),
  ];

  return (
    <div className="flex min-w-0 flex-col gap-6">
      <PageHeader
        title="Audit log"
        freshness={list.dataUpdatedAt > 0 ? { at: new Date(list.dataUpdatedAt) } : null}
      />
      <FilterBar
        collapsed
        actions={
          <DayRange
            from={from}
            to={to}
            error={daysError}
            onFrom={(d) => setOne("from", dayToUtc(d, false))}
            onTo={(d) => setOne("to", dayToUtc(d, true))}
            onClear={() => {
              const next = new URLSearchParams(params);
              next.delete("from");
              next.delete("to");
              setParams(next, { replace: true });
            }}
          />
        }
        search={search}
        onSearch={setSearch}
        searchLabel="Search the log"
        placeholder="Search the log"
        filters={filters}
        onClearAll={
          filtered
            ? () => {
                setSearch("");
                clear();
              }
            : undefined
        }
      />
      {daysError ? (
        <p role="alert" className="text-small text-critical">
          {daysError}
        </p>
      ) : null}
      {daysError ? null : list.isPending ? (
        <LoadingState label="Loading the audit log">
          <Skeleton.Table rows={8} columns={2} />
        </LoadingState>
      ) : list.error ? (
        <ErrorState
          title="The audit log did not load"
          error={list.error}
          onRetry={() => list.refetch()}
        />
      ) : (
        <>
          <AuditEntries
            entries={entries}
            onClear={
              filtered
                ? () => {
                    setSearch("");
                    clear();
                  }
                : undefined
            }
          />
          {entries.length > 0 ? (
            <div className="flex flex-wrap items-center justify-between gap-3">
              <p className="text-small text-fg-2 tabular" aria-live="polite" data-count="">
                {`${countOf(entries.length, "entry", "entries")} shown.`}
              </p>
              {list.hasNextPage ? (
                <Button
                  variant="outline"
                  onClick={() => list.fetchNextPage()}
                  pending={list.isFetchingNextPage}
                  className="max-lg:h-(--vs-control-touch)"
                >
                  {list.isFetchingNextPage ? "Loading" : "Load more"}
                </Button>
              ) : null}
            </div>
          ) : null}
        </>
      )}
    </div>
  );
}

function textFilter(
  key: "tenant_id" | "actor" | "target",
  label: string,
  hint: string,
  q: AuditQuery,
  set: (f: Field, v: string) => void,
): Filter {
  return {
    key,
    label,
    control: <TextFilter label={label} hint={hint} value={q[key]} onChange={(v) => set(key, v)} />,
    active: q[key] ? { label: q[key], onClear: () => set(key, "") } : null,
  };
}

// a text filter applies after a short pause in typing
function TextFilter({
  label,
  hint,
  value,
  onChange,
}: {
  label: string;
  hint: string;
  value: string;
  onChange: (v: string) => void;
}) {
  const [draft, setDraft] = useState(value);
  useEffect(() => setDraft(value), [value]);
  useEffect(() => {
    const t = draft.trim();
    if (t === value) return;
    const timer = setTimeout(() => onChange(t), TEXT_DELAY_MS);
    return () => clearTimeout(timer);
  });
  return (
    <TextField
      label={label}
      hint={hint}
      optional
      spellCheck={false}
      autoComplete="off"
      className="font-mono max-lg:h-(--vs-control-touch)"
      value={draft}
      onChange={(e) => setDraft(e.target.value)}
    />
  );
}

// the day range is one button next to Filters: it names the range, and
// opens the two utc days, each applied as soon as it is a whole day
function DayRange({
  from,
  to,
  error,
  onFrom,
  onTo,
  onClear,
}: {
  from: string;
  to: string;
  error: string | null;
  onFrom: (d: string) => void;
  onTo: (d: string) => void;
  onClear: () => void;
}) {
  const [open, setOpen] = useState(false);
  const label =
    from && to
      ? `${formatDate(from)} to ${formatDate(to)}`
      : from
        ? `From ${formatDate(from)}`
        : to
          ? `To ${formatDate(to)}`
          : "Any day";
  return (
    <>
      <Button
        type="button"
        variant="outline"
        aria-haspopup="dialog"
        onClick={() => setOpen(true)}
        className="max-lg:h-(--vs-control-touch)"
        data-day-range=""
      >
        <CalendarDays aria-hidden />
        {label}
      </Button>
      <Dialog
        open={open}
        onOpenChange={setOpen}
        title="Days"
        size="sm"
        footer={
          <>
            {from || to ? (
              <Button type="button" variant="ghost" onClick={onClear}>
                Any day
              </Button>
            ) : null}
            <Button type="button" onClick={() => setOpen(false)}>
              Done
            </Button>
          </>
        }
      >
        <div className="flex flex-col gap-3">
          <DayFilter label="From" value={from} onChange={onFrom} />
          <DayFilter label="To, included" value={to} error={error} onChange={onTo} />
        </div>
      </Dialog>
    </>
  );
}

// a day applies once the field holds a whole day, or nothing
function DayFilter({
  label,
  value,
  error,
  onChange,
}: {
  label: string;
  value: string;
  error?: string | null;
  onChange: (v: string) => void;
}) {
  const [draft, setDraft] = useState(value);
  useEffect(() => setDraft(value), [value]);
  return (
    <DateField
      label={label}
      optional
      value={draft}
      error={error}
      onChange={(v) => {
        setDraft(v);
        const t = v.trim();
        if (t !== value && (t === "" || isIsoDay(t))) onChange(t);
      }}
    />
  );
}

// ---- entries

function dayLabel(day: string, today: string): string {
  return day === today ? `Today, ${formatDate(day)}` : formatDate(day);
}

// the log in utc days, newest first; a run of the same system event folds
// into one line. `limit` shows only the newest entries (the project page).
export function AuditEntries({
  entries,
  onClear,
  limit,
}: {
  entries: AuditEntry[];
  onClear?: () => void;
  limit?: number;
}) {
  if (entries.length === 0)
    return (
      <EmptyState
        title={onClear ? "No entry matches these filters" : "No entry yet"}
        action={
          onClear ? (
            <Button variant="outline" size="sm" onClick={onClear}>
              Clear filters
            </Button>
          ) : null
        }
      />
    );
  const shown = limit ? entries.slice(0, limit) : entries;
  const today = new Date().toISOString().slice(0, 10);
  const groups: { day: string; items: AuditEntry[] }[] = [];
  for (const e of shown) {
    const day = e.at.slice(0, 10);
    const last = groups.at(-1);
    if (last && last.day === day) last.items.push(e);
    else groups.push({ day, items: [e] });
  }
  return (
    <ol
      aria-label="Audit entries"
      className="flex min-w-0 flex-col overflow-hidden rounded-2xl border border-line bg-raised"
    >
      {groups.map((g) => (
        <li key={g.day} className="flex flex-col">
          <h3 className="border-b border-line bg-sunken px-5 py-2 text-caption font-semibold text-fg-3">
            {dayLabel(g.day, today)}, UTC
          </h3>
          <ol className="flex flex-col">
            {foldRuns(g.items).map((item) => (
              <li
                key={item.kind === "one" ? item.entry.id : `run-${item.key}`}
                className="border-b border-line last:border-b-0"
              >
                <ItemRow item={item} />
              </li>
            ))}
          </ol>
        </li>
      ))}
    </ol>
  );
}

// a run of the same system event is one row with its count as a badge;
// the row opens to every entry of the run
function ItemRow({ item }: { item: AuditItem }) {
  if (item.kind === "one") return <AuditRow e={item.entry} />;
  const first = item.entries[0] as AuditEntry;
  const last = item.entries.at(-1) as AuditEntry;
  return (
    <AuditRow
      e={first}
      run={{ count: item.entries.length, from: last.at, entries: item.entries }}
    />
  );
}

// a person is the round avatar; the system and an ai assistant
// are square marks with an icon, so they never look like a person
function ActorMark({ e }: { e: AuditEntry }) {
  const s = auditSentence(e);
  if (s.kind === "person" && e.actor_login)
    return <Avatar name={e.actor_login} className="size-6" />;
  const Icon = s.kind === "system" ? Cog : s.kind === "assistant" ? Bot : User;
  return (
    <span
      aria-hidden
      data-actor={s.kind}
      className="flex size-6 items-center justify-center rounded-full border border-dashed border-line-strong text-fg-3"
    >
      <Icon className="size-3.5" strokeWidth={1.75} />
    </span>
  );
}

// the actor in bold; the job of a system entry ("(purge)") stays plain,
// as on the canvas
function ActorName({ actor }: { actor: string }) {
  const m = /^(.*?)( \(.+\))$/.exec(actor);
  if (!m) return <span className="font-semibold text-fg">{actor}</span>;
  return (
    <>
      <span className="font-semibold text-fg">{m[1]}</span>
      {m[2]}
    </>
  );
}

const GRID =
  "grid grid-cols-[3.25rem_1.5rem_minmax(0,1fr)_auto] gap-x-3 px-4 sm:grid-cols-[5rem_1.5rem_minmax(0,1fr)_auto] sm:px-5";

// one entry: the time first, the actor, then the sentence. a reason given
// with the action is a quoted line; a setting change is an old to new chip.
// the row opens in place to the codes and the raw detail; a run opens to
// each of its entries.
function AuditRow({
  e,
  nested,
  run,
}: {
  e: AuditEntry;
  nested?: boolean;
  run?: { count: number; from: string; entries: AuditEntry[] };
}) {
  const s = auditSentence(e);
  const changes = run ? [] : auditChanges(e);
  const reason = run ? null : auditReason(e);
  const [open, setOpen] = useState(false);
  const id = useId();
  const verb = run ? s.verb.replace(/ (on|of|for|from|to)$/, "") : s.verb;
  const at = formatTimeUtc(e.at).replace(" UTC", "");
  const runFrom = run ? formatTimeUtc(run.from).replace(" UTC", "") : null;
  return (
    <article
      data-action={e.action}
      data-run={run ? run.count : undefined}
      data-open={open ? "" : undefined}
      className={cn("flex flex-col", open && !nested && "bg-hover")}
    >
      {/* biome-ignore lint/a11y/useKeyWithClickEvents: the chevron button is the keyboard way to open the row */}
      {/* biome-ignore lint/a11y/noStaticElementInteractions: a click anywhere on the row opens it, as the chevron does */}
      <div
        onClick={(ev) => {
          if ((ev.target as HTMLElement).closest("a, button")) return;
          setOpen((o) => !o);
        }}
        className={cn(
          GRID,
          "cursor-pointer items-start py-3",
          !open && "transition-colors duration-(--vs-duration-press) ease-standard hover:bg-hover",
        )}
      >
        <time
          dateTime={e.at}
          title={
            run
              ? `${formatDateTimeUtc(run.from)} to ${formatDateTimeUtc(e.at)}`
              : formatDateTimeUtc(e.at)
          }
          className="pt-0.5 text-small text-fg-3 tabular"
        >
          {run && runFrom !== at ? (
            <>
              <span className="sr-only">{runFrom} to </span>
              {at}
            </>
          ) : (
            at
          )}
        </time>
        <ActorMark e={e} />
        <div className="flex min-w-0 flex-col gap-1.5">
          <p className="text-body break-words text-fg-2">
            <ActorName actor={s.actor} /> {verb}
            {s.object && !run ? (
              <>
                {" "}
                {s.href ? (
                  <Link
                    to={s.href}
                    className="rounded-sm font-semibold text-fg underline-offset-4 hover:underline focus-ring"
                  >
                    {s.object}
                  </Link>
                ) : (
                  <span className="font-semibold text-fg">{s.object}</span>
                )}
              </>
            ) : null}
            {run ? (
              <span
                className="ml-2 inline-flex rounded-sm bg-selected px-1.5 align-[1px] font-mono text-axis text-fg-2 leading-5"
                data-run-count=""
              >
                <span aria-hidden>×</span>
                {run.count}
                <span className="sr-only"> times</span>
              </span>
            ) : null}
          </p>
          {reason ? (
            <q className="text-small break-words text-fg-2" data-reason="">
              <span className="sr-only">Reason: </span>
              {reason}
            </q>
          ) : null}
          {changes.length > 0 ? (
            <div className="flex flex-wrap items-center gap-1.5">
              {changes.map((c) => (
                <span
                  key={c.key}
                  className="inline-flex max-w-full items-center gap-1.5 rounded-md border border-line-2 px-2 py-0.5 font-mono text-axis leading-5"
                >
                  <span className="text-fg-3">{c.key}</span>
                  <span className="sr-only">changed from</span>
                  <span className="text-fg-3 line-through">{c.from}</span>
                  <span aria-hidden className="text-fg-3">
                    →
                  </span>
                  <span className="sr-only">to</span>
                  <span className="text-fg">{c.to}</span>
                </span>
              ))}
            </div>
          ) : null}
        </div>
        <Button
          variant="ghost"
          size="icon-sm"
          aria-expanded={open}
          aria-controls={id}
          aria-label={
            run
              ? open
                ? "Hide the entries"
                : "Show the entries"
              : open
                ? "Hide the details"
                : "Show the details"
          }
          onClick={() => setOpen((o) => !o)}
          className={cn("-mt-0.5 max-lg:size-(--vs-control-touch)", open && "bg-selected")}
        >
          <ChevronDown
            aria-hidden
            className={cn(
              "transition-transform duration-(--vs-duration-change) ease-standard",
              open && "rotate-180",
            )}
          />
        </Button>
      </div>
      {open && run ? (
        <ol id={id} className="animate-fade-in border-t border-line bg-sunken">
          {run.entries.map((x) => (
            <li key={x.id} className="border-b border-line last:border-b-0">
              <AuditRow e={x} nested />
            </li>
          ))}
        </ol>
      ) : open ? (
        <div id={id} className={cn(GRID, "animate-fade-in pb-4 text-small")}>
          <span aria-hidden />
          <span aria-hidden />
          <div className="min-w-0">
            <dl className="grid grid-cols-[5rem_minmax(0,1fr)] gap-x-3 gap-y-1.5">
              <dt className="text-fg-3">Action</dt>
              <dd className="font-mono text-small break-all text-fg">{e.action}</dd>
              <dt className="text-fg-3">Actor</dt>
              <dd className="font-mono text-small break-all text-fg">{e.actor}</dd>
              {e.target ? (
                <>
                  <dt className="text-fg-3">Target</dt>
                  <dd className="font-mono text-small break-all text-fg">{e.target}</dd>
                </>
              ) : null}
            </dl>
            {e.detail && Object.keys(e.detail).length > 0 ? (
              <pre className="mt-3 overflow-x-auto rounded-lg border border-line bg-sunken p-3 font-mono text-small text-fg-2">
                {JSON.stringify(e.detail, null, 2)}
              </pre>
            ) : null}
          </div>
          <span aria-hidden />
        </div>
      ) : (
        <span id={id} hidden />
      )}
    </article>
  );
}
