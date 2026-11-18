import { useInfiniteQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { ArrowRight, Bot, ChevronDown, Cog, ScrollText } from "lucide-react";
import { useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router";
import type { AuditQuery } from "@/api/endpoints";
import { auditQuery } from "@/api/queries";
import type { AuditEntry } from "@/api/schemas";
import { type Filter, FilterBar } from "@/components/filter-bar";
import { SelectField, TextField } from "@/components/form-field";
import { PageHeader } from "@/components/page-header";
import { DateField, isIsoDay } from "@/components/range-control";
import { EmptyState, ErrorState, LoadingState, Skeleton } from "@/components/states";
import { Button } from "@/components/ui/button";
import {
  ACTION_FAMILIES,
  type AuditItem,
  auditChanges,
  auditDaysError,
  auditReason,
  auditSentence,
  dayToUtc,
  foldRuns,
  initials,
  utcToDay,
} from "@/lib/audit";
import { countOf, formatDate, formatDateTimeUtc, formatTimeUtc } from "@/lib/format";

const FIELDS = ["tenant_id", "action", "actor", "target", "from", "to"] as const;
type Field = (typeof FIELDS)[number];

export function auditFromParams(p: URLSearchParams): AuditQuery {
  return {
    tenant_id: p.get("tenant_id") ?? "",
    action: p.get("action") ?? "",
    actor: p.get("actor") ?? "",
    target: p.get("target") ?? "",
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
  const filtered = FIELDS.some((f) => q[f] !== "");

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
    {
      key: "from",
      label: "From",
      control: (
        <DayFilter label="From" value={from} onChange={(d) => setOne("from", dayToUtc(d, false))} />
      ),
      active: q.from ? { label: formatDate(from), onClear: () => setOne("from", "") } : null,
    },
    {
      key: "to",
      label: "To",
      control: (
        <DayFilter
          label="To, included"
          value={to}
          error={daysError}
          onChange={(d) => setOne("to", dayToUtc(d, true))}
        />
      ),
      active: q.to ? { label: formatDate(to), onClear: () => setOne("to", "") } : null,
    },
  ];

  return (
    <div className="flex min-w-0 flex-col gap-6">
      <PageHeader
        title="Audit log"
        description="Every action of an operator, a project member or the system. Newest first."
        freshness={list.dataUpdatedAt > 0 ? { at: new Date(list.dataUpdatedAt) } : null}
      />
      <FilterBar collapsed filters={filters} onClearAll={filtered ? clear : undefined} />
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
          <AuditEntries entries={entries} onClear={filtered ? clear : undefined} />
          {entries.length > 0 ? (
            <div className="flex flex-wrap items-center justify-between gap-3">
              <p className="text-small text-fg-2" aria-live="polite" data-count="">
                {countOf(entries.length, "entry", "entries")} shown
                {list.hasNextPage ? ". Older entries load on request." : "."}
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
  return day === today ? "Today" : formatDate(day);
}

// the log in utc days, newest first; a run of the same system event folds
// into one line. `limit` shows only the newest entries (the project page).
// `names` names the targets the page knows ("site:<id>" to its domain).
export function AuditEntries({
  entries,
  onClear,
  limit,
  names,
}: {
  entries: AuditEntry[];
  onClear?: () => void;
  limit?: number;
  names?: Readonly<Record<string, string>>;
}) {
  if (entries.length === 0)
    return (
      <EmptyState
        icon={<ScrollText aria-hidden strokeWidth={1.75} />}
        title={onClear ? "No entry matches these filters" : "No entry yet"}
        action={
          onClear ? (
            <Button variant="outline" size="sm" onClick={onClear}>
              Clear filters
            </Button>
          ) : null
        }
      >
        {onClear ? "Change or clear the filters." : "Actions show here when they happen."}
      </EmptyState>
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
      className="flex min-w-0 flex-col overflow-hidden rounded-lg border border-line bg-raised"
    >
      {groups.map((g) => (
        <li key={g.day} className="flex flex-col">
          <h3 className="border-b border-line-subtle px-4 pt-3 pb-1 text-caption text-fg-2">
            {dayLabel(g.day, today)}
          </h3>
          <ol className="flex flex-col">
            {foldRuns(g.items).map((item) => (
              <li key={item.kind === "one" ? item.entry.id : `run-${item.key}`}>
                <ItemRow item={item} names={names} />
              </li>
            ))}
          </ol>
        </li>
      ))}
    </ol>
  );
}

type Names = Readonly<Record<string, string>> | undefined;

function ItemRow({ item, names }: { item: AuditItem; names: Names }) {
  if (item.kind === "one") return <AuditRow e={item.entry} names={names} />;
  const first = item.entries[0] as AuditEntry;
  const last = item.entries.at(-1) as AuditEntry;
  const s = auditSentence(first, names);
  // a run inside one minute shows that minute once
  const from = formatTimeUtc(last.at);
  const to = formatTimeUtc(first.at);
  return (
    <details className="group border-b border-line-subtle" data-run={item.entries.length}>
      <summary className="grid cursor-pointer grid-cols-[1.75rem_minmax(0,1fr)_auto] items-start gap-x-3 px-4 py-3 transition-colors duration-(--vs-duration-press) ease-standard hover:bg-hover focus-ring">
        <ActorMark e={first} />
        <span className="min-w-0 text-body text-fg-2">
          <span className="font-medium text-fg">{s.actor}</span>{" "}
          {s.verb.replace(/ (on|of|for|from|to)$/, "")},{" "}
          <span className="font-medium text-fg">{countOf(item.entries.length, "time")}</span>
          <span className="block text-caption">{from === to ? to : `${from} to ${to}`}</span>
        </span>
        <ChevronDown
          aria-hidden
          className="mt-1 size-4 text-fg-2 transition-transform duration-(--vs-duration-change) ease-standard group-open:rotate-180"
        />
      </summary>
      <ol className="border-t border-line-subtle bg-sunken">
        {item.entries.map((e) => (
          <li key={e.id}>
            <AuditRow e={e} names={names} nested />
          </li>
        ))}
      </ol>
    </details>
  );
}

// a person is a round mark with initials; the system and an ai assistant
// are square marks with an icon, so they never look like a person
function ActorMark({ e }: { e: AuditEntry }) {
  const s = auditSentence(e);
  if (s.kind === "person")
    return (
      <span
        aria-hidden
        className="flex size-7 items-center justify-center rounded-full border border-line-strong bg-selected text-axis font-semibold text-fg"
      >
        {e.actor_login ? initials(e.actor_login) : "?"}
      </span>
    );
  const Icon = s.kind === "system" ? Cog : Bot;
  return (
    <span
      aria-hidden
      data-actor={s.kind}
      className="flex size-7 items-center justify-center rounded-md border border-dashed border-line-strong text-fg-2"
    >
      <Icon className="size-4" strokeWidth={1.75} />
    </span>
  );
}

function AuditRow({ e, names, nested }: { e: AuditEntry; names: Names; nested?: boolean }) {
  const s = auditSentence(e, names);
  const changes = auditChanges(e);
  const reason = auditReason(e);
  return (
    <article
      data-action={e.action}
      className={cn(
        "grid grid-cols-[1.75rem_minmax(0,1fr)] gap-x-3 gap-y-1 px-4 py-3 sm:grid-cols-[1.75rem_minmax(0,1fr)_auto]",
        !nested &&
          "border-b border-line-subtle transition-colors duration-(--vs-duration-press) ease-standard hover:bg-hover",
      )}
    >
      <ActorMark e={e} />
      <div className="flex min-w-0 flex-col gap-1">
        <p className="text-body break-words text-fg-2">
          <span className={cn("font-medium", s.kind === "person" ? "text-fg" : "text-fg-2")}>
            {s.actor}
          </span>{" "}
          {s.verb}
          {s.object ? (
            <>
              {" "}
              {s.href ? (
                <Link
                  to={s.href}
                  className="rounded-sm font-medium text-fg underline-offset-4 hover:underline focus-ring"
                >
                  {s.object}
                </Link>
              ) : (
                <span className="font-medium text-fg">{s.object}</span>
              )}
            </>
          ) : null}
          .
        </p>
        {changes.length > 0 || reason ? (
          <div className="flex flex-wrap items-center gap-1">
            {changes.map((c) => (
              <span
                key={c.key}
                className="inline-flex max-w-full items-center gap-1 rounded-sm border border-line px-1 text-caption"
              >
                <span className="text-fg-2">{c.key}</span>
                <span className="sr-only">changed from</span>
                <span className="text-fg-2 line-through">{c.from}</span>
                <ArrowRight aria-hidden className="size-3 shrink-0 text-fg-3" />
                <span className="sr-only">to</span>
                <span className="text-fg">{c.to}</span>
              </span>
            ))}
            {reason ? (
              <q className="text-caption break-words text-fg-2">
                <span className="sr-only">Reason: </span>
                {reason}
              </q>
            ) : null}
          </div>
        ) : null}
        <details className="text-caption">
          <summary className="w-fit cursor-pointer rounded-sm text-fg-2 transition-colors duration-(--vs-duration-press) ease-standard hover:text-fg focus-ring">
            Details
          </summary>
          <dl className="mt-1 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1">
            <dt className="text-fg-2">Action</dt>
            <dd className="font-mono break-all text-fg">{e.action}</dd>
            <dt className="text-fg-2">Actor</dt>
            <dd className="font-mono break-all text-fg">{e.actor}</dd>
            {e.target ? (
              <>
                <dt className="text-fg-2">Target</dt>
                <dd className="font-mono break-all text-fg">{e.target}</dd>
              </>
            ) : null}
          </dl>
          <pre className="mt-2 overflow-x-auto rounded-md border border-line bg-sunken p-2 font-mono text-caption text-fg-2">
            {JSON.stringify(e.detail ?? {}, null, 2)}
          </pre>
        </details>
      </div>
      <time
        dateTime={e.at}
        title={formatDateTimeUtc(e.at)}
        className="col-start-2 font-mono text-caption text-fg-2 tabular sm:col-start-3 sm:row-start-1"
      >
        {formatTimeUtc(e.at)}
      </time>
    </article>
  );
}
