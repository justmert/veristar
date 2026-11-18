import { useInfiniteQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { ArrowRight, ScrollText, X } from "lucide-react";
import { type FormEvent, useState } from "react";
import { Link, useSearchParams } from "react-router";
import type { AuditQuery } from "@/api/endpoints";
import { auditQuery } from "@/api/queries";
import type { AuditEntry } from "@/api/schemas";
import { ErrorPanel, Loading, StateBlock } from "@/components/error-panel";
import { TextField } from "@/components/form-field";
import { DateField } from "@/components/range-control";
import { Button } from "@/components/ui/button";
import { formatCount, formatDate, formatDateTimeUtc, formatTimeUtc, MISSING } from "@/lib/format";

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
// 5.9: from before to, at most 400 days apart; the server refuses anything
// else with invalid_query, so the form says it first
export function auditDaysError(from: string, to: string): string | null {
  if (!from || !to) return null;
  if (to < from) return "End on or after the start day.";
  if ((Date.parse(to) - Date.parse(from)) / 86_400_000 + 1 > 400)
    return "The days cover at most 400 days.";
  return null;
}

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

const FIELD_LABEL: Record<Field, string> = {
  tenant_id: "Project",
  action: "Action",
  actor: "Actor",
  target: "Target",
  from: "From",
  to: "Before",
};

// one click narrows the log to a family of actions (the action filter takes
// a prefix ending in a dot)
const QUICK = [
  "tenant.",
  "site.",
  "member.",
  "erasure.",
  "mcp.",
  "operator.",
  "cohort.",
  "rule.",
  "report.",
  "onchain.",
  "export.",
  "user.",
];

const EMPTY_DRAFT: Record<Field, string> = {
  tenant_id: "",
  action: "",
  actor: "",
  target: "",
  from: "",
  to: "",
};

// the operator layout draws the place header and its tabs; this page starts
// with its own section header
export function AuditPage() {
  const [params, setParams] = useSearchParams();
  const q = auditFromParams(params);
  const [draft, setDraft] = useState<Record<Field, string>>({
    ...q,
    from: utcToDay(q.from, false),
    to: utcToDay(q.to, true),
  });
  const list = useInfiniteQuery(auditQuery(q));
  const [dayError, setDayError] = useState<string | null>(null);
  const active = FIELDS.filter((f) => q[f] !== "");

  function apply(next: URLSearchParams) {
    setParams(next, { replace: true });
  }

  function submit(e: FormEvent) {
    e.preventDefault();
    const bad = auditDaysError(draft.from.trim(), draft.to.trim());
    setDayError(bad);
    if (bad) return;
    const next = new URLSearchParams();
    for (const f of FIELDS) {
      const raw = draft[f].trim();
      const value = f === "from" ? dayToUtc(raw, false) : f === "to" ? dayToUtc(raw, true) : raw;
      if (value) next.set(f, value);
    }
    apply(next);
  }

  // a quick filter or a removed chip changes the applied filters at once and
  // keeps the form in step with them
  function setOne(f: Field, value: string) {
    const next = new URLSearchParams(params);
    if (value) next.set(f, value);
    else next.delete(f);
    setDraft((s) => ({
      ...s,
      [f]: f === "from" ? utcToDay(value, false) : f === "to" ? utcToDay(value, true) : value,
    }));
    apply(next);
  }

  function clear() {
    setDraft(EMPTY_DRAFT);
    apply(new URLSearchParams());
  }

  const set = (f: Field) => (e: React.ChangeEvent<HTMLInputElement>) =>
    setDraft((s) => ({ ...s, [f]: e.target.value }));

  return (
    <section aria-labelledby="audit-h" className="flex animate-rise-in flex-col gap-5">
      <header className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
        <div className="flex flex-col gap-1">
          <h2 id="audit-h" className="text-section text-fg">
            Audit log
          </h2>
          <p className="max-w-prose text-body text-fg-2">
            Every decision by an operator, a project owner or the system, newest first.
          </p>
        </div>
        <span className="text-caption text-fg-3">Times in UTC</span>
      </header>

      <fieldset className="flex flex-wrap items-center gap-2">
        <legend className="sr-only">Quick filters</legend>
        <QuickChip on={q.action === ""} onClick={() => setOne("action", "")}>
          All
        </QuickChip>
        {QUICK.map((a) => (
          <QuickChip key={a} on={q.action === a} onClick={() => setOne("action", a)} mono>
            {a}
          </QuickChip>
        ))}
        {active.length > 0 ? (
          <>
            <span aria-hidden className="mx-1 h-5 w-px bg-line" />
            {active.map((f) => (
              <span
                key={f}
                className="inline-flex h-7 max-w-full animate-fade-in items-center gap-1 rounded-md border border-line-strong pr-1 pl-2 text-small"
              >
                <span className="text-fg-3">{FIELD_LABEL[f]}</span>
                <span className="min-w-0 truncate font-mono text-caption text-fg">
                  {f === "from" || f === "to" ? utcToDay(q[f], false) : q[f]}
                </span>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-xs"
                  aria-label={`Remove filter ${FIELD_LABEL[f]}`}
                  onClick={() => setOne(f, "")}
                >
                  <X aria-hidden />
                </Button>
              </span>
            ))}
          </>
        ) : null}
      </fieldset>

      <form
        noValidate
        onSubmit={submit}
        aria-label="Filter the audit log"
        className="grid gap-4 rounded-xl border border-line bg-raised p-4 sm:grid-cols-2 sm:px-5 lg:grid-cols-3"
      >
        <TextField
          label="Project id"
          name="tenant_id"
          optional
          spellCheck={false}
          className="font-mono max-lg:h-(--vs-control-touch)"
          value={draft.tenant_id}
          onChange={set("tenant_id")}
        />
        <TextField
          label="Action"
          name="action"
          optional
          spellCheck={false}
          className="font-mono max-lg:h-(--vs-control-touch)"
          hint="Exact, or a prefix ending in a dot, like tenant."
          value={draft.action}
          onChange={set("action")}
        />
        <TextField
          label="Actor"
          name="actor"
          optional
          spellCheck={false}
          className="font-mono max-lg:h-(--vs-control-touch)"
          hint="Like user:<id> or system:purge."
          value={draft.actor}
          onChange={set("actor")}
        />
        <TextField
          label="Target"
          name="target"
          optional
          spellCheck={false}
          className="font-mono max-lg:h-(--vs-control-touch)"
          hint="Like tenant:<id> or site:<id>."
          value={draft.target}
          onChange={set("target")}
        />
        <DateField
          label="From (UTC day)"
          name="from"
          optional
          value={draft.from}
          onChange={(v) => setDraft((s) => ({ ...s, from: v }))}
        />
        <DateField
          label="To (UTC day, included)"
          name="to"
          optional
          value={draft.to}
          onChange={(v) => setDraft((s) => ({ ...s, to: v }))}
          error={dayError}
        />
        <div className="flex flex-wrap items-end gap-2 sm:col-span-2 lg:col-span-3">
          <Button type="submit" className="max-lg:h-(--vs-control-touch)">
            Apply filters
          </Button>
          <Button
            type="button"
            variant="ghost"
            className="max-lg:h-(--vs-control-touch)"
            onClick={clear}
          >
            Clear
          </Button>
        </div>
      </form>

      {list.isPending ? (
        <Loading label="Loading the audit log" rows={5} />
      ) : list.error ? (
        <ErrorPanel error={list.error} onRetry={() => list.refetch()} />
      ) : (
        <>
          <AuditEntries
            entries={list.data.pages.flatMap((p) => p.items)}
            onClear={active.length > 0 ? clear : undefined}
          />
          {list.hasNextPage ? (
            <div className="flex justify-center">
              <Button
                variant="outline"
                onClick={() => list.fetchNextPage()}
                pending={list.isFetchingNextPage}
                className="max-lg:h-(--vs-control-touch)"
              >
                {list.isFetchingNextPage ? "Loading" : "Load more"}
              </Button>
            </div>
          ) : null}
        </>
      )}
    </section>
  );
}

function QuickChip({
  on,
  onClick,
  mono,
  children,
}: {
  on: boolean;
  onClick: () => void;
  mono?: boolean;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      aria-pressed={on}
      onClick={onClick}
      className={cn(
        "inline-flex h-7 items-center rounded-full border px-3 text-small transition-colors duration-(--vs-duration-press) ease-standard focus-ring max-lg:h-(--vs-control-touch)",
        mono && "font-mono text-caption",
        on
          ? "border-accent-text bg-selected text-fg"
          : "border-line text-fg-2 hover:border-line-strong hover:text-fg",
      )}
    >
      {children}
    </button>
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

// one side of a change as a person reads it: a list or an object is spelt
// out (never "[object Object]"), a missing value is the missing glyph
export function changeValue(v: unknown): string {
  if (v === null || v === undefined || v === "") return MISSING;
  if (typeof v === "boolean") return v ? "on" : "off";
  if (typeof v === "number") return formatCount(v);
  if (Array.isArray(v)) return v.length ? v.map(changeValue).join(", ") : MISSING;
  if (isObj(v)) {
    const parts = Object.entries(v).map(([k, x]) => `${k.replaceAll("_", " ")} ${changeValue(x)}`);
    return parts.length ? parts.join(", ") : MISSING;
  }
  return String(v);
}

// every { old, new } pair of the detail, drawn as a change chip
export function auditChanges(e: AuditEntry): { key: string; from: string; to: string }[] {
  const out: { key: string; from: string; to: string }[] = [];
  for (const [key, v] of Object.entries(e.detail ?? {})) {
    if (isObj(v) && "old" in v && "new" in v)
      out.push({ key: key.replaceAll("_", " "), from: changeValue(v.old), to: changeValue(v.new) });
  }
  return out;
}

// the action as the verb of a sentence; an action this list does not know
// is shown by its code, so nothing is hidden
const VERB: Record<string, string> = {
  "tenant.create": "enrolled project",
  "tenant.update": "edited project",
  "tenant.resubmit": "resubmitted project",
  "tenant.approve": "approved project",
  "tenant.reject": "rejected project",
  "tenant.suspend": "suspended project",
  "tenant.resume": "resumed project",
  "tenant.remove": "removed project",
  "tenant.terms_accept": "accepted the terms for project",
  "site.create": "added site",
  "site.update": "edited site",
  "site.delete": "deleted site",
  "site.verify": "verified site",
  "site.approve": "approved site",
  "site.reject": "rejected site",
  "member.add": "added member",
  "member.remove": "removed member",
  "member.role_change": "changed the role of",
  "invite.create": "invited",
  "invite.revoke": "revoked the invite of",
  "invite.resolve": "resolved the invite of",
  "erasure.request": "requested erasure on",
  "erasure.complete": "completed erasure on",
  "erasure.fail": "recorded a failed erasure on",
  "mcp.tool_call": "called an MCP tool on",
  "mcp_credential.create": "created MCP credential",
  "mcp_credential.revoke": "revoked MCP credential",
  "operator_api_key.create": "created API key",
  "operator_api_key.revoke": "revoked API key",
  "otlp_token.create": "created OTLP token",
  "otlp_token.revoke": "revoked OTLP token",
  "settings.enrollment_mode": "changed the enrollment mode",
  "settings.removal_keep_aggregates": "changed the removal setting",
  "umami.password_rotate": "rotated dashboard passwords",
  "user.sign_in": "signed in",
  "user.sign_out": "signed out",
  "user.identity_link": "linked a sign-in method to",
  "user.identity_unlink": "unlinked a sign-in method from",
  "user.operator_sync": "synced the operator flag of",
  "operator.add": "added operator",
  "operator.bootstrap": "added operator from the deploy settings",
  "operator.remove": "removed operator",
  "operator.suspend": "suspended operator",
  "operator.resume": "resumed operator",
  "operator.role_change": "changed the operator role of",
  "operator.passkey_add": "added an operator passkey",
  "operator.passkey_delete": "deleted an operator passkey",
  "operator.passkeys_reset": "reset the passkeys of",
  "operator.step_up": "confirmed with a passkey",
  "cohort.set": "set the cohort of",
  "cohort.propose": "proposed a cohort for",
  "cohort.confirm": "confirmed the cohort of",
  "cohort.proposal_dismiss": "dismissed the cohort proposal of",
  "rule.create": "created rule",
  "rule.update": "edited rule",
  "rule.delete": "deleted rule",
  "report.create": "signed a report for",
  "report.revoke": "revoked a report link of",
  "report.view": "viewed report",
  "onchain.add": "declared an on-chain id for",
  "onchain.remove": "removed an on-chain id from",
  "onchain.challenge": "started on-chain verification for",
  "onchain.verify": "verified an on-chain id of",
  "export.csv": "exported CSV of",
};

export function auditVerb(action: string): string {
  return VERB[action] ?? `recorded ${action}`;
}

function verbOf(e: AuditEntry): string {
  const tool = e.action === "mcp.tool_call" ? detailText(e, "tool") : null;
  const verb = tool ? `called the MCP tool ${tool} on` : auditVerb(e.action);
  // without a target the sentence ends at the verb
  return e.target ? verb : verb.replace(/ (on|of)$/, "");
}

function targetLink(target: string): string | null {
  const m = /^tenant:([0-9a-f-]{36})$/.exec(target);
  return m ? `/operator/projects/${m[1]}` : null;
}

// a person by their login; an ai client by what it is (its credential id
// stays in the title and the raw json); the system by its job name
function actorName(e: AuditEntry): string {
  if (e.actor_login) return e.actor_login;
  if (e.actor.startsWith("mcp_credential:")) return "An MCP client";
  return e.actor;
}

function initials(e: AuditEntry): string {
  if (e.actor_login)
    return (
      e.actor_login
        .replace(/[^0-9A-Za-z]/g, "")
        .slice(0, 2)
        .toUpperCase() || "?"
    );
  return e.actor.startsWith("mcp_credential:") ? "AI" : "SY";
}

function detailText(e: AuditEntry, key: string): string | null {
  const v = e.detail?.[key];
  return typeof v === "string" && v !== "" ? v : null;
}

function dayLabel(iso: string, today: string): string {
  const day = iso.slice(0, 10);
  return day === today ? "Today" : formatDate(`${day}T00:00:00Z`);
}

export function AuditEntries({
  entries,
  onClear,
}: {
  entries: AuditEntry[];
  // offered in the empty state when filters narrowed the log
  onClear?: () => void;
}) {
  if (entries.length === 0)
    return (
      <StateBlock
        kind="empty"
        icon={<ScrollText aria-hidden strokeWidth={1.75} />}
        title="No audit entries match."
        action={
          onClear ? (
            <Button variant="outline" onClick={onClear}>
              Clear filters
            </Button>
          ) : null
        }
      >
        {onClear ? "Widen or clear the filters to see more of the log." : null}
      </StateBlock>
    );
  // entries come newest first; a group starts at each new utc day
  const today = new Date().toISOString().slice(0, 10);
  const groups: { day: string; items: AuditEntry[] }[] = [];
  for (const e of entries) {
    const day = e.at.slice(0, 10);
    const last = groups[groups.length - 1];
    if (last && last.day === day) last.items.push(e);
    else groups.push({ day, items: [e] });
  }
  return (
    <ol
      aria-label="Audit entries"
      className="flex animate-rise-in flex-col overflow-hidden rounded-xl border border-line bg-raised"
    >
      {groups.map((g) => (
        <li key={g.day} className="flex flex-col">
          <h3 className="border-b border-line-subtle bg-raised px-4 pt-3 pb-1 text-caption font-medium tracking-wide text-fg-3 uppercase">
            {dayLabel(g.items[0]?.at ?? g.day, today)}
          </h3>
          <ol className="flex flex-col">
            {g.items.map((e) => (
              <li key={e.id}>
                <AuditRow e={e} />
              </li>
            ))}
          </ol>
        </li>
      ))}
    </ol>
  );
}

function AuditRow({ e }: { e: AuditEntry }) {
  const link = targetLink(e.target);
  const system = !e.actor_login;
  const changes = auditChanges(e);
  const name = detailText(e, "name") ?? detailText(e, "domain") ?? detailText(e, "github_login");
  const reason =
    typeof e.detail?.reason === "string" && e.detail.reason !== "" ? e.detail.reason : null;
  return (
    <article className="grid grid-cols-[1.75rem_minmax(0,1fr)] gap-x-3 gap-y-1 border-b border-line-subtle px-4 py-3 transition-colors duration-(--vs-duration-press) ease-standard hover:bg-hover sm:grid-cols-[1.75rem_minmax(0,1fr)_auto]">
      <span
        aria-hidden
        className={cn(
          "flex size-7 items-center justify-center rounded-full border border-line-strong text-axis font-semibold",
          system ? "bg-sunken text-fg-3" : "bg-muted text-fg",
        )}
      >
        {initials(e)}
      </span>
      <div className="flex min-w-0 flex-col gap-1">
        <p className="text-body break-words">
          <span className={cn("font-semibold", system ? "text-fg-2" : "text-fg")} title={e.actor}>
            {actorName(e)}
          </span>{" "}
          <span className="text-fg-2">{verbOf(e)}</span>
          {e.target ? (
            <>
              {" "}
              {name ? <span className="font-medium text-fg">{name} </span> : null}
              {link ? (
                <Link
                  to={link}
                  className={cn(
                    "rounded-sm font-mono underline-offset-2 hover:underline focus-ring",
                    name ? "text-caption text-fg-3" : "text-small font-medium text-fg",
                  )}
                >
                  {e.target}
                </Link>
              ) : (
                <span
                  className={cn(
                    "font-mono break-all",
                    name ? "text-caption text-fg-3" : "text-small font-medium text-fg",
                  )}
                >
                  {e.target}
                </span>
              )}
            </>
          ) : null}
        </p>
        {changes.length > 0 || reason ? (
          <div className="flex flex-wrap items-center gap-1">
            {changes.map((c) => (
              <span
                key={c.key}
                className="inline-flex items-center gap-1 rounded-sm border border-line px-1 py-px text-caption font-normal"
              >
                <span className="text-fg-3">{c.key}</span>
                <span className="sr-only">changed from</span>
                <span className="text-fg-2 line-through">{c.from}</span>
                <ArrowRight aria-hidden className="size-2.5 text-fg-3" />
                <span className="sr-only">to</span>
                <span className="text-fg">{c.to}</span>
              </span>
            ))}
            {reason ? (
              <q className="text-caption font-normal text-fg-2">
                <span className="sr-only">Reason: </span>
                {reason}
              </q>
            ) : null}
          </div>
        ) : null}
        <details className="group">
          <summary className="w-fit cursor-pointer rounded-sm text-caption font-normal text-fg-3 transition-colors duration-(--vs-duration-press) ease-standard hover:text-fg focus-ring">
            Raw JSON
          </summary>
          <pre className="mt-1 animate-rise-in overflow-x-auto rounded-md border border-line bg-sunken p-2 font-mono text-caption text-fg-2">
            {JSON.stringify(e, null, 2)}
          </pre>
        </details>
      </div>
      <div className="col-start-2 flex flex-wrap items-baseline gap-x-3 gap-y-1 sm:col-start-3 sm:row-start-1 sm:flex-col sm:items-end">
        <time
          dateTime={e.at}
          title={formatDateTimeUtc(e.at)}
          className="font-mono text-caption text-fg-2 tabular"
        >
          {formatTimeUtc(e.at)}
        </time>
        <span className="font-mono text-axis text-fg-3">{e.action}</span>
      </div>
    </article>
  );
}
