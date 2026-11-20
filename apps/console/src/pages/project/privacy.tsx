import { useInfiniteQuery, useMutation, useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { type FormEvent, type ReactNode, useEffect, useId, useState } from "react";
import { ApiError } from "@/api/client";
import { api, type ErasureInput } from "@/api/endpoints";
import {
  erasureListQuery,
  erasureRequestQuery,
  keys,
  membersQuery,
  sitesQuery,
} from "@/api/queries";
import type { ErasureKind, ErasureRequest, Site } from "@/api/schemas";
import { type WebBand, webSessionsQuery } from "@/api/web";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { type Column, DataTable } from "@/components/data-table";
import { FormMessage } from "@/components/error-panel";
import { TextField } from "@/components/form-field";
import { Notice } from "@/components/notice";
import { PageHeader } from "@/components/page-header";
import { Section } from "@/components/panel";
import { DateField } from "@/components/range-control";
import { Segmented } from "@/components/segmented";
import { EmptyState, ErrorState } from "@/components/states";
import { Pill, type Tone } from "@/components/status";
import { toast } from "@/components/toast";
import { Button } from "@/components/ui/button";
import { Skeleton as Bone } from "@/components/ui/skeleton";
import {
  ipField,
  requestSummary,
  sessionField,
  userAgentField,
  utcToday,
} from "@/lib/erasure";
import { countOf, formatCount, formatDate, formatDateTimeUtc, formatTimeRange } from "@/lib/format";
import { siblingUrl } from "@/lib/hosts";
import { fieldMessage } from "@/lib/validate";
import { keyLabel } from "@/lib/web-dashboard";
import { useProject } from "@/pages/project-layout";
import { queryClient } from "@/query-client";
import { PAGE_HEADERS, PrivacySkeleton } from "./page-frame";

// the form's values. ip and userAgent are personal data the owner types on
// behalf of a visitor: they live only in this react state, are sent once in
// the request body and are cleared as soon as the request is answered. they
// never go to localStorage, sessionStorage, a url, the query cache or the
// browser's form history (autocomplete is off on both fields).
export type ErasureValues = {
  kind: ErasureKind;
  siteId: string;
  ip: string;
  userAgent: string;
  sessionId: string;
};
export type ErasureErrors = Partial<
  Record<"siteId" | "ip" | "userAgent" | "sessionId" | "confirmDomain", string>
>;

const EMPTY = { ip: "", userAgent: "", sessionId: "" };

export function validateErasure(values: ErasureValues, site: Site | undefined): ErasureErrors {
  const errors: ErasureErrors = {};
  const set = (k: keyof ErasureErrors, code: string | null) => {
    if (code) errors[k] = fieldMessage(code);
  };
  if (!site) set("siteId", "required");
  switch (values.kind) {
    case "visitor_today":
      set("ip", ipField(values.ip));
      set("userAgent", userAgentField(values.userAgent));
      break;
    case "session":
      set("sessionId", sessionField(values.sessionId));
      break;
    case "site_reset":
      // the domain is typed in the confirmation dialog
      break;
  }
  return errors;
}

// the request body for the kind: only that kind's fields, trimmed. the
// gateway reads the user agent from a header, which http trims, so a pasted
// trailing space would otherwise compute another visitor's token. a site
// reset carries the domain the owner typed in the confirmation dialog.
export function erasureBody(values: ErasureValues, domain: string): ErasureInput {
  switch (values.kind) {
    case "visitor_today":
      return {
        kind: "visitor_today",
        site_id: values.siteId,
        ip: values.ip.trim(),
        user_agent: values.userAgent.trim(),
      };
    case "session":
      return {
        kind: "session",
        site_id: values.siteId,
        session_id: values.sessionId.trim().toLowerCase(),
      };
    case "site_reset":
      return {
        kind: "site_reset",
        site_id: values.siteId,
        confirm_domain: domain,
      };
  }
}

// 422 field codes of contract 10.2 on the form's fields. the server's
// messages never quote the address or user agent, and neither does this.
export function serverErasureErrors(err: unknown): ErasureErrors {
  if (!(err instanceof ApiError) || err.code !== "validation_failed") return {};
  const map: Record<string, keyof ErasureErrors> = {
    site_id: "siteId",
    ip: "ip",
    user_agent: "userAgent",
    session_id: "sessionId",
    confirm_domain: "confirmDomain",
  };
  const out: ErasureErrors = {};
  for (const [path, code] of Object.entries(err.fields)) {
    const k = map[path];
    if (k)
      out[k] ??=
        k === "siteId" && code === "invalid_value"
          ? "Not a site of this project."
          : fieldMessage(code);
  }
  return out;
}

// the answers of contract 10.2 that are not field errors, in the page's words
export function erasureErrorText(err: unknown): string | null {
  if (!(err instanceof ApiError)) return null;
  switch (err.code) {
    case "erasure_in_progress":
      return "Another erasure request for this site is still running. Wait for it to finish, then send this one.";
    case "site_removed":
      return "This site was deleted, and its web data was deleted with it.";
    case "provisioning_pending":
      return "The web analytics of this site are still being set up, so it holds no web data yet. Try again in a minute.";
    case "unavailable":
      return "The ingestion service does not answer, so nothing was erased. Try again in a minute.";
    default:
      return null;
  }
}

// the three kinds in the page's words, with the scope of each in a short
// word group
export const KIND_TEXT: Record<ErasureKind, { label: string; scope: string }> = {
  visitor_today: { label: "One visitor's visits today", scope: "by IP address and browser" },
  session: { label: "One session", scope: "any day raw data is kept" },
  site_reset: { label: "Everything of this site", scope: "events, sessions and daily totals" },
};
const KINDS: ErasureKind[] = ["visitor_today", "session", "site_reset"];

export function PrivacyPage() {
  const { tenant, isOwner } = useProject();
  const sites = useQuery(sitesQuery(tenant.id));
  const docs = siblingUrl(window.location.hostname, "docs");

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        {...PAGE_HEADERS.privacy}
        items={
          docs
            ? [
                <a
                  key="how"
                  href={`${docs}/compliance/erasure`}
                  className="rounded-sm underline decoration-line-strong underline-offset-[3px] transition-colors duration-(--vs-duration-press) hover:text-fg focus-ring"
                >
                  How erasure works
                </a>,
              ]
            : undefined
        }
      />
      {sites.isPending ? (
        <PrivacySkeleton />
      ) : sites.error ? (
        <ErrorState
          title="The sites did not load"
          error={sites.error}
          onRetry={() => sites.refetch()}
        />
      ) : (
        <PrivacyBody tenantId={tenant.id} isOwner={isOwner} all={sites.data} />
      )}
    </div>
  );
}

function PrivacyBody({
  tenantId,
  isOwner,
  all,
}: {
  tenantId: string;
  isOwner: boolean;
  all: Site[];
}) {
  const live = all.filter((s) => s.status !== "removed");
  return (
    <div className="flex min-w-0 flex-col gap-8">
      {live.length === 0 ? (
        <EmptyState title="No site, so no web data to erase" />
      ) : isOwner ? (
        <ErasureForm tenantId={tenantId} sites={live} />
      ) : (
        <Notice tone="info" title="Owners request erasure">
          Only owners of this project can request erasure. The history below shows each request.
        </Notice>
      )}
      <History tenantId={tenantId} sites={all} />
    </div>
  );
}

// the confirmation of each kind: what is deleted, and for a whole site the
// domain to type
function confirmText(kind: ErasureKind, domain: string): { title: string; description: string } {
  switch (kind) {
    case "visitor_today":
      return {
        title: `Erase one visitor on ${domain}?`,
        description:
          "The sessions and events of this visitor from today are deleted. You cannot undo this.",
      };
    case "session":
      return {
        title: `Erase this session on ${domain}?`,
        description: "The session and its events are deleted. You cannot undo this.",
      };
    case "site_reset":
      return {
        title: `Reset all web data of ${domain}?`,
        description:
          "All events and sessions of this site are deleted, counted and quarantined, with their daily totals. The site keeps collecting new visits. You cannot undo this.",
      };
  }
}

const CONFIRM_LABEL: Record<ErasureKind, string> = {
  visitor_today: "Erase data",
  session: "Erase data",
  site_reset: "Reset site data",
};

// one numbered question of the form: its number and words on the left, the
// answer on the right; stacked on a phone
function Question({
  n,
  label,
  id,
  children,
}: {
  n: number;
  label: string;
  id: string;
  children: ReactNode;
}) {
  return (
    <div className="grid gap-x-6 gap-y-3 border-b border-line px-6 py-5 max-sm:px-4 md:grid-cols-[220px_minmax(0,1fr)]">
      <span className="flex items-center gap-3 self-start md:pt-1.5">
        <span
          aria-hidden
          className="flex size-[22px] shrink-0 items-center justify-center rounded-full border border-line-strong text-caption text-fg-2 tabular"
        >
          {n}
        </span>
        <span id={id} className="text-body font-medium text-fg">
          {label}
        </span>
      </span>
      <div className="flex min-w-0 flex-col gap-3">{children}</div>
    </div>
  );
}

// a radio group drawn as rows inside one frame: a dot, the words and an
// optional quiet part on the right. arrow keys move the choice, as in a
// native radio group.
function RadioRows<T extends string>({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: T | null;
  options: { value: T; cells: ReactNode }[];
  onChange: (v: T) => void;
}) {
  const at = options.findIndex((o) => o.value === value);
  function key(e: React.KeyboardEvent<HTMLButtonElement>, i: number) {
    const d =
      e.key === "ArrowDown" || e.key === "ArrowRight"
        ? 1
        : e.key === "ArrowUp" || e.key === "ArrowLeft"
          ? -1
          : 0;
    if (!d) return;
    e.preventDefault();
    const j = (i + d + options.length) % options.length;
    const next = options[j];
    if (!next) return;
    onChange(next.value);
    (e.currentTarget.parentElement?.children[j] as HTMLElement | undefined)?.focus();
  }
  return (
    <div
      role="radiogroup"
      aria-label={label}
      className="flex flex-col divide-y divide-line-subtle overflow-hidden rounded-xl border border-line"
    >
      {options.map((o, i) => {
        const on = o.value === value;
        return (
          // biome-ignore lint/a11y/useSemanticElements: a row with several cells, keyed like a native radio group
          <button
            key={o.value}
            type="button"
            role="radio"
            aria-checked={on}
            tabIndex={on || (at === -1 && i === 0) ? 0 : -1}
            onClick={() => onChange(o.value)}
            onKeyDown={(e) => key(e, i)}
            className={cn(
              "flex min-h-11 w-full flex-wrap items-center gap-x-4 gap-y-1 px-3.5 py-2.5 text-left text-body transition-colors duration-(--vs-duration-press) ease-standard focus-ring focus-visible:-outline-offset-2",
              on ? "bg-hover" : "hover:bg-hover",
            )}
          >
            <span
              aria-hidden
              className={cn(
                "flex size-4 shrink-0 items-center justify-center rounded-full border",
                on ? "border-fg" : "border-line-strong",
              )}
            >
              {on ? <span className="size-2 rounded-full bg-fg" /> : null}
            </span>
            {o.cells}
          </button>
        );
      })}
    </div>
  );
}

function ErasureForm({ tenantId, sites }: { tenantId: string; sites: Site[] }) {
  const [values, setValues] = useState<ErasureValues>({
    kind: "visitor_today",
    siteId: sites.length === 1 ? (sites[0]?.id ?? "") : "",
    ...EMPTY,
  });
  const [errors, setErrors] = useState<ErasureErrors>({});
  const [confirming, setConfirming] = useState(false);
  const [lastId, setLastId] = useState<string | null>(null);
  const site = sites.find((s) => s.id === values.siteId);
  const ids = { site: useId(), kind: useId(), who: useId() };

  const send = useMutation({
    mutationFn: (body: ErasureInput) => api.requestErasure(tenantId, body),
    onSuccess: async (r) => {
      setConfirming(false);
      setLastId(r.id);
      toast.success("The erasure request is sent.");
      // the typed values are not needed any more; reset() below drops the
      // mutation's own copy of the body from the observer
      setValues((s) => ({ ...s, ...EMPTY }));
      send.reset();
      await queryClient.invalidateQueries({ queryKey: keys.erasure(tenantId) });
    },
    // the typed values stay in the form so the owner can correct them; like
    // the mutation's copy of the body they are in memory only, since the
    // console persists neither react state nor the query cache
    onError: (err) => {
      const fields = serverErasureErrors(err);
      setErrors(fields);
      // a field error is shown on its field; the dialog closes for it. the
      // typed domain lives in the dialog, so its error stays there.
      if (Object.keys(fields).some((k) => k !== "confirmDomain")) setConfirming(false);
    },
  });

  function change(patch: Partial<ErasureValues>) {
    setValues((s) => ({ ...s, ...patch }));
    if (send.error) send.reset();
  }

  function submit(e: FormEvent) {
    e.preventDefault();
    const found = validateErasure(values, site);
    setErrors(found);
    if (Object.keys(found).length > 0) return;
    send.reset();
    setConfirming(true);
  }

  const fieldError = Object.keys(serverErasureErrors(send.error)).length > 0;
  const special = erasureErrorText(send.error);
  const domain = site?.domain ?? "the site";
  const text = confirmText(values.kind, domain);
  // two sites of one name are told apart by their domain
  const names = new Map<string, number>();
  for (const x of sites) names.set(x.name, (names.get(x.name) ?? 0) + 1);

  return (
    <section aria-label="Request erasure" className="flex min-w-0 flex-col gap-4">
      <form
        noValidate
        autoComplete="off"
        onSubmit={submit}
        aria-label="Request erasure"
        className="flex min-w-0 flex-col overflow-hidden rounded-2xl border border-line bg-raised"
      >
        <Question n={1} label="Site" id={ids.site}>
          <div role="radiogroup" aria-labelledby={ids.site} className="flex flex-wrap gap-2">
            {sites.map((s) => {
              const on = s.id === values.siteId;
              return (
                // biome-ignore lint/a11y/useSemanticElements: a site chip that wraps, keyed like a native radio group
                <button
                  key={s.id}
                  type="button"
                  role="radio"
                  aria-checked={on}
                  title={s.domain}
                  // a session id belongs to one site; a chosen one does not carry over
                  onClick={() => change({ siteId: s.id, sessionId: "" })}
                  className={cn(
                    "inline-flex h-9 max-w-full items-center rounded-full border px-3.5 text-small transition-colors duration-(--vs-duration-press) focus-ring max-lg:h-(--vs-control-touch)",
                    on
                      ? "border-fg-2 text-fg"
                      : "border-line text-fg-2 hover:bg-hover hover:text-fg",
                  )}
                >
                  <span className="truncate">
                    {(names.get(s.name) ?? 0) > 1 ? s.domain : s.name}
                  </span>
                </button>
              );
            })}
          </div>
          {errors.siteId ? (
            <p role="alert" className="text-small text-critical">
              {errors.siteId === fieldMessage("required") ? "Choose a site." : errors.siteId}
            </p>
          ) : null}
        </Question>

        <Question n={2} label="What to erase" id={ids.kind}>
          <RadioRows<ErasureKind>
            label="What to erase"
            value={values.kind}
            onChange={(kind) => change({ kind })}
            options={KINDS.map((k) => ({
              value: k,
              cells: (
                <>
                  <span className="min-w-0 flex-1 text-fg">{KIND_TEXT[k].label}</span>
                  <span className="text-small text-fg-3">{KIND_TEXT[k].scope}</span>
                </>
              ),
            }))}
          />
        </Question>

        {values.kind === "visitor_today" ? (
          <Question n={3} label="Who" id={ids.who}>
            <TextField
              label="IP address"
              name="erasure-ip"
              autoComplete="off"
              autoCapitalize="none"
              spellCheck={false}
              inputMode="text"
              className="font-mono max-lg:h-(--vs-control-touch)"
              placeholder="203.0.113.7"
              value={values.ip}
              onChange={(e) => change({ ip: e.target.value })}
              error={errors.ip}
            />
            <TextField
              label="User agent"
              name="erasure-user-agent"
              autoComplete="off"
              autoCapitalize="none"
              spellCheck={false}
              className="font-mono max-lg:h-(--vs-control-touch)"
              placeholder="Mozilla/5.0 (X11; Linux x86_64) ..."
              value={values.userAgent}
              onChange={(e) => change({ userAgent: e.target.value })}
              error={errors.userAgent}
            />
          </Question>
        ) : values.kind === "session" ? (
          <Question n={3} label="Which session" id={ids.who}>
            {site ? (
              <SessionFinder
                tenantId={site.tenant_id}
                site={site}
                chosen={values.sessionId}
                onChoose={(id) => change({ sessionId: id })}
              />
            ) : null}
            <TextField
              label="Session id"
              name="erasure-session"
              autoComplete="off"
              autoCapitalize="none"
              spellCheck={false}
              className="font-mono max-lg:h-(--vs-control-touch)"
              placeholder="2f1c7a9e-5b3d-4c8e-9a1f-6d0b2e4c8a7f"
              value={values.sessionId}
              onChange={(e) => change({ sessionId: e.target.value })}
              error={errors.sessionId}
            />
          </Question>
        ) : null}

        <div className="flex flex-col gap-3 px-6 py-4 max-sm:px-4">
          <Button
            type="submit"
            variant="destructive-outline"
            className="w-fit max-lg:h-(--vs-control-touch)"
          >
            Review and erase
          </Button>
          {confirming || fieldError ? null : special ? (
            <FormMessage error={new Error(special)} />
          ) : (
            <FormMessage error={send.error} />
          )}
        </div>
      </form>

      <ConfirmDialog
        open={confirming}
        onOpenChange={(o) => {
          setConfirming(o);
          if (!o) send.reset();
        }}
        title={text.title}
        description={text.description}
        cancelLabel="Keep the data"
        confirmLabel={CONFIRM_LABEL[values.kind]}
        pendingLabel="Sending"
        pending={send.isPending}
        error={special ? new Error(special) : (errors.confirmDomain ?? send.error)}
        typed={
          values.kind === "site_reset" && site
            ? { value: site.domain, label: `Type ${site.domain} to confirm` }
            : undefined
        }
        onConfirm={() => site && send.mutate(erasureBody(values, site.domain))}
      />

      {lastId ? <LastRequest tenantId={tenantId} requestId={lastId} site={site} /> : null}
    </section>
  );
}

// the request just sent, followed until the control plane finishes it
function LastRequest({
  tenantId,
  requestId,
  site,
}: {
  tenantId: string;
  requestId: string;
  site: Site | undefined;
}) {
  const q = useQuery(erasureRequestQuery(tenantId, requestId));
  const status = q.data?.status;
  // the history shows the finished row as soon as this one finishes
  useEffect(() => {
    if (status && status !== "running")
      void queryClient.invalidateQueries({ queryKey: keys.erasure(tenantId) });
  }, [status, tenantId]);
  if (q.error)
    return (
      <ErrorState
        title="The erasure request did not load"
        error={q.error}
        onRetry={() => q.refetch()}
        compact
      />
    );
  if (!q.data)
    return (
      <div role="status" aria-busy="true">
        <span className="sr-only">Reading the request</span>
        <Bone className="h-12 w-full" />
      </div>
    );
  const r = q.data;
  const summary = requestSummary(r);
  return (
    <section
      aria-label="Latest erasure request"
      aria-live="polite"
      className="flex flex-wrap items-center gap-x-4 gap-y-2 rounded-xl border border-line bg-raised px-4 py-3 text-body"
    >
      <SummaryPill r={r} />
      <span className="font-medium text-fg">
        {`${KIND_TEXT[r.kind].label} on ${site?.domain ?? "the site"}: ${summary.toLowerCase()}`}
      </span>
      {r.status !== "running" && (r.message ?? r.error) ? (
        <span className="text-small text-fg-2">{r.message ?? r.error}</span>
      ) : null}
    </section>
  );
}

const LOOK: Record<string, Tone> = {
  Running: "info",
  Erased: "success",
  "Nothing found": "neutral",
  Failed: "critical",
};

// the status of a request: a pill, since running and failed are the states
// to notice and a finished one closes the row
function SummaryPill({ r }: { r: ErasureRequest }) {
  const summary = requestSummary(r);
  return (
    <Pill tone={LOOK[summary] ?? "neutral"} live={summary === "Running"}>
      {summary === "Erased" ? "Done" : summary}
    </Pill>
  );
}

// what a finished request deleted, and the state of the daily totals
export function deletedText(r: ErasureRequest): string {
  if (r.status === "running" || r.status === "pending") return "";
  const parts = [
    `Deleted ${countOf(r.sessions_deleted, "session")}, ${countOf(r.events_deleted, "event")} and ${countOf(r.dlq_messages_deleted, "undeliverable queued message")}.`,
  ];
  if (r.affected_from)
    parts.push(
      r.reaggregated_at
        ? `Daily aggregates were recomputed ${formatDateTimeUtc(r.reaggregated_at)}.`
        : "Daily aggregates are being recomputed.",
    );
  return parts.join(" ");
}

function target(r: ErasureRequest): string | null {
  if (r.kind === "visitor_today") return r.day ? `Visitor of ${formatDate(r.day)} (UTC)` : null;
  if (r.kind === "session") return r.session_id;
  return null;
}

function History({ tenantId, sites }: { tenantId: string; sites: Site[] }) {
  const list = useInfiniteQuery(erasureListQuery(tenantId));
  const people = useQuery(membersQuery(tenantId));
  const names = new Map(sites.map((s) => [s.id, s.name]));
  const who = new Map((people.data?.members ?? []).map((m) => [m.user_id, m.display_name]));
  const rows = list.data?.pages.flatMap((p) => p.items) ?? [];
  const columns: Column<ErasureRequest>[] = [
    {
      key: "requested",
      header: "Requested",
      mobile: true,
      cell: (r) => (
        <span className="whitespace-nowrap text-fg-2">{formatDateTimeUtc(r.requested_at)}</span>
      ),
    },
    {
      key: "what",
      header: "What",
      primary: true,
      minWidth: "min-w-64",
      className: "w-[34%]",
      cell: (r) => {
        const detail = [r.message, deletedText(r)].filter(Boolean);
        const t = target(r);
        return (
          <span className="flex min-w-0 flex-col gap-1 py-2">
            <span className="font-medium text-fg">{KIND_TEXT[r.kind].label}</span>
            {t ? <span className="break-all font-mono text-caption text-fg-3">{t}</span> : null}
            {detail.map((d) => (
              <span key={d} className="text-small font-normal text-fg-2">
                {d}
              </span>
            ))}
            {r.error ? (
              <span className="text-small font-normal text-critical">{r.error}</span>
            ) : null}
          </span>
        );
      },
    },
    {
      key: "site",
      header: "Site",
      cell: (r) => <span className="text-fg-2">{names.get(r.site_id) ?? "A deleted site"}</span>,
    },
    {
      key: "by",
      header: "By",
      cell: (r) => (
        <span className="text-fg-2">{who.get(r.requested_by) ?? "A former member"}</span>
      ),
    },
    {
      key: "status",
      header: "Status",
      mobile: true,
      cell: (r) => (
        <span className="flex flex-col items-start gap-1">
          <SummaryPill r={r} />
          {r.completed_at ? (
            <span className="whitespace-nowrap text-caption text-fg-3">
              {formatDateTimeUtc(r.completed_at)}
            </span>
          ) : null}
        </span>
      ),
    },
  ];
  return (
    <Section title="Request history" count={list.data ? rows.length : null}>
      <DataTable<ErasureRequest>
        label="Request history"
        columns={columns}
        rows={rows}
        getRowKey={(r) => r.id}
        rowLabel={(r) => `${KIND_TEXT[r.kind].label} ${r.id}`}
        rowProps={(r) => ({ "data-erasure": r.id })}
        state={list.isPending ? "loading" : list.error ? "error" : "data"}
        error={list.error}
        onRetry={() => list.refetch()}
        loadingRows={3}
        empty={{ title: "No erasure requested yet" }}
        footer={{
          kind: "more",
          hasMore: list.hasNextPage,
          loading: list.isFetchingNextPage,
          onMore: () => void list.fetchNextPage(),
        }}
      />
    </Section>
  );
}

// the sessions of one site and utc day, newest first, so an owner can find
// the visit a person describes by its time, pages and browser. read live
// from the analytics engine; only the chosen id goes into the request.
export function SessionFinder({
  tenantId,
  site,
  chosen,
  onChoose,
}: {
  tenantId: string;
  site: Site;
  chosen: string;
  onChoose: (id: string) => void;
}) {
  const [day, setDay] = useState(utcToday(new Date()));
  const [page, setPage] = useState(1);
  const [band, setBand] = useState<WebBand>("primary");
  const q = useQuery(webSessionsQuery(tenantId, site.id, day, page, band));
  const pages = q.data ? Math.max(1, Math.ceil(q.data.total / q.data.page_size)) : 1;
  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-end gap-3">
        <div className="w-44">
          <DateField
            label="Day of the visit (UTC)"
            value={day}
            onChange={(v) => {
              if (v) setDay(v);
              setPage(1);
            }}
          />
        </div>
        {site.quarantine_website_id ? (
          <Segmented<WebBand>
            label="Traffic"
            value={band}
            options={[
              { value: "primary", label: "Counted" },
              { value: "quarantine", label: "Quarantined" },
            ]}
            onChange={(b) => {
              setBand(b);
              setPage(1);
            }}
          />
        ) : null}
      </div>
      {q.isPending ? (
        <div role="status" aria-busy="true" className="flex flex-col gap-2">
          <span className="sr-only">Loading sessions</span>
          <Bone className="h-10 w-full" />
          <Bone className="h-10 w-full" />
        </div>
      ) : q.error ? (
        <ErrorState
          title="The sessions did not load"
          error={q.error}
          onRetry={() => q.refetch()}
          compact
        />
      ) : q.data.items.length === 0 ? (
        <p className="text-small text-fg-2">No sessions on this day.</p>
      ) : (
        <>
          <div className="max-h-80 overflow-y-auto rounded-xl">
            <RadioRows<string>
              label="Sessions"
              value={chosen || null}
              onChange={onChoose}
              options={q.data.items.map((x) => ({
                value: x.id,
                cells: (
                  <>
                    <span className="w-28 shrink-0 text-fg tabular">
                      {formatTimeRange(x.first_at, x.last_at)}
                    </span>
                    <span className="min-w-0 flex-1 truncate text-small text-fg-2">
                      {[
                        x.browser && keyLabel("browser", x.browser),
                        x.os,
                        x.device && keyLabel("device", x.device),
                        x.country && keyLabel("country", x.country),
                      ]
                        .filter(Boolean)
                        .join(", ")}
                    </span>
                    <span className="shrink-0 text-small text-fg-2 tabular">
                      {formatCount(x.views)} {x.views === 1 ? "page view" : "page views"}
                    </span>
                  </>
                ),
              }))}
            />
          </div>
          {pages > 1 ? (
            <div className="flex items-center justify-between text-caption text-fg-3">
              <span>
                Page {page} of {pages}
              </span>
              <span className="flex gap-2">
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  disabled={page === 1}
                  onClick={() => setPage(page - 1)}
                >
                  Newer
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  disabled={page >= pages}
                  onClick={() => setPage(page + 1)}
                >
                  Older
                </Button>
              </span>
            </div>
          ) : null}
        </>
      )}
    </div>
  );
}
