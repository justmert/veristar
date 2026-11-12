import { useInfiniteQuery } from "@tanstack/react-query";
import { type FormEvent, useState } from "react";
import { Link, useSearchParams } from "react-router";
import type { AuditQuery } from "@/api/endpoints";
import { auditQuery } from "@/api/queries";
import type { AuditEntry } from "@/api/schemas";
import { ErrorPanel, Loading } from "@/components/error-panel";
import { TextField } from "@/components/form-field";
import { Button } from "@/components/ui/button";
import { formatDateTimeUtc } from "@/lib/format";

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

// a date input gives a local calendar day; the audit filter takes rfc 3339
// in utc (contract 5.9), so the day is read as a utc day. "to" is the start
// of the day after, so the chosen day is included.
export function dayToUtc(day: string, end: boolean): string {
  if (day === "") return "";
  const d = new Date(`${day}T00:00:00Z`);
  if (Number.isNaN(d.getTime())) return "";
  if (end) d.setUTCDate(d.getUTCDate() + 1);
  return d.toISOString().replace(/\.\d{3}Z$/, "Z");
}

function utcToDay(iso: string, end: boolean): string {
  if (iso === "") return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  if (end) d.setUTCDate(d.getUTCDate() - 1);
  return d.toISOString().slice(0, 10);
}

export function AuditPage() {
  const [params, setParams] = useSearchParams();
  const q = auditFromParams(params);
  const [draft, setDraft] = useState<Record<Field, string>>({
    ...q,
    from: utcToDay(q.from, false),
    to: utcToDay(q.to, true),
  });
  const list = useInfiniteQuery(auditQuery(q));

  function submit(e: FormEvent) {
    e.preventDefault();
    const next = new URLSearchParams();
    for (const f of FIELDS) {
      const raw = draft[f].trim();
      const value = f === "from" ? dayToUtc(raw, false) : f === "to" ? dayToUtc(raw, true) : raw;
      if (value) next.set(f, value);
    }
    setParams(next, { replace: true });
  }

  const set = (f: Field) => (e: React.ChangeEvent<HTMLInputElement>) =>
    setDraft((s) => ({ ...s, [f]: e.target.value }));

  return (
    <section className="space-y-5">
      <h2 className="text-xl font-semibold tracking-tight">Audit log</h2>
      <form
        noValidate
        onSubmit={submit}
        aria-label="Filter the audit log"
        className="grid gap-4 rounded-xl border bg-card p-4 sm:grid-cols-2 sm:p-5 lg:grid-cols-3"
      >
        <TextField
          label="Project id"
          name="tenant_id"
          optional
          spellCheck={false}
          value={draft.tenant_id}
          onChange={set("tenant_id")}
        />
        <TextField
          label="Action"
          name="action"
          optional
          spellCheck={false}
          hint="Exact, or a prefix ending in a dot, like tenant."
          value={draft.action}
          onChange={set("action")}
        />
        <TextField
          label="Actor"
          name="actor"
          optional
          spellCheck={false}
          hint="Like user:<id> or system:purge."
          value={draft.actor}
          onChange={set("actor")}
        />
        <TextField
          label="Target"
          name="target"
          optional
          spellCheck={false}
          hint="Like tenant:<id> or site:<id>."
          value={draft.target}
          onChange={set("target")}
        />
        <TextField
          label="From (UTC day)"
          name="from"
          optional
          type="date"
          value={draft.from}
          onChange={set("from")}
        />
        <TextField
          label="To (UTC day, included)"
          name="to"
          optional
          type="date"
          value={draft.to}
          onChange={set("to")}
        />
        <div className="flex items-end gap-2 sm:col-span-2 lg:col-span-3">
          <Button type="submit">Apply filters</Button>
          <Button
            type="button"
            variant="ghost"
            onClick={() => {
              setDraft({ tenant_id: "", action: "", actor: "", target: "", from: "", to: "" });
              setParams(new URLSearchParams(), { replace: true });
            }}
          >
            Clear
          </Button>
        </div>
      </form>

      {list.isPending ? (
        <Loading label="Loading the audit log" />
      ) : list.error ? (
        <ErrorPanel error={list.error} onRetry={() => list.refetch()} />
      ) : (
        <>
          <AuditEntries entries={list.data.pages.flatMap((p) => p.items)} />
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

function isObj(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

// the parts of an audit detail a person reads first: the reason and a status
// change. the whole detail stays available below each row.
export function auditSummary(e: AuditEntry): string[] {
  const out: string[] = [];
  const d = e.detail ?? {};
  const status = d.status;
  if (isObj(status) && typeof status.old === "string" && typeof status.new === "string")
    out.push(`Status ${status.old} to ${status.new}`);
  const mode = d.mode;
  if (isObj(mode) && typeof mode.old === "string" && typeof mode.new === "string")
    out.push(`Mode ${mode.old} to ${mode.new}`);
  if (typeof d.reason === "string" && d.reason !== "") out.push(`Reason: ${d.reason}`);
  return out;
}

function targetLink(target: string): string | null {
  const m = /^tenant:([0-9a-f-]{36})$/.exec(target);
  return m ? `/operator/tenants/${m[1]}` : null;
}

export function AuditEntries({ entries }: { entries: AuditEntry[] }) {
  if (entries.length === 0)
    return (
      <p className="rounded-xl border border-dashed bg-card p-6 text-muted-foreground">
        No audit entries match.
      </p>
    );
  return (
    <ol className="divide-y overflow-hidden rounded-xl border bg-card" aria-label="Audit entries">
      {entries.map((e) => {
        const link = targetLink(e.target);
        return (
          <li key={e.id} className="space-y-1 p-4 text-sm">
            <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
              <span className="font-mono text-xs font-medium">{e.action}</span>
              <span className="text-muted-foreground">{formatDateTimeUtc(e.at)}</span>
              <span className="text-muted-foreground">
                by {e.actor_login ? `${e.actor_login} (${e.actor})` : e.actor}
              </span>
            </div>
            {e.target ? (
              <p className="break-all text-xs text-muted-foreground">
                Target:{" "}
                {link ? (
                  <Link to={link} className="underline underline-offset-2 hover:text-foreground">
                    {e.target}
                  </Link>
                ) : (
                  e.target
                )}
              </p>
            ) : null}
            {auditSummary(e).map((line) => (
              <p key={line} className="break-words">
                {line}
              </p>
            ))}
            {e.detail && Object.keys(e.detail).length > 0 ? (
              <details>
                <summary className="cursor-pointer text-xs text-muted-foreground">Detail</summary>
                <pre className="mt-1 overflow-x-auto rounded-md bg-muted p-2 font-mono text-xs">
                  {JSON.stringify(e.detail, null, 2)}
                </pre>
              </details>
            ) : null}
          </li>
        );
      })}
    </ol>
  );
}
