import { useInfiniteQuery, useMutation, useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { fade } from "@veristar/brand/motion";
import { Check, Eraser, ExternalLink, Minus, X } from "lucide-react";
import { motion } from "motion/react";
import { type FormEvent, type ReactNode, useEffect, useState } from "react";
import { ApiError } from "@/api/client";
import { api, type ErasureInput } from "@/api/endpoints";
import { erasureListQuery, erasureRequestQuery, keys, sitesQuery } from "@/api/queries";
import type { ErasureKind, ErasureRequest, Site } from "@/api/schemas";
import { type WebBand, webSessionsQuery } from "@/api/web";
import { ChoiceCard } from "@/components/choice-card";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { type Column, DataTable } from "@/components/data-table";
import { FormMessage } from "@/components/error-panel";
import { SelectField, TextAreaField, TextField } from "@/components/form-field";
import { Notice } from "@/components/notice";
import { PageHeader } from "@/components/page-header";
import { Section } from "@/components/panel";
import { DateField } from "@/components/range-control";
import { Segmented } from "@/components/segmented";
import { EmptyState, ErrorState } from "@/components/states";
import { StatusDot, type Tone } from "@/components/status";
import { toast } from "@/components/toast";
import { Button } from "@/components/ui/button";
import { Skeleton as Bone } from "@/components/ui/skeleton";
import {
  ipField,
  KIND_LABEL,
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

export function PrivacyPage() {
  const { tenant, isOwner } = useProject();
  const sites = useQuery(sitesQuery(tenant.id));
  const docs = siblingUrl(window.location.hostname, "docs");

  return (
    <div className="flex flex-col gap-8">
      <PageHeader
        {...PAGE_HEADERS.privacy}
        secondary={
          docs ? (
            <Button asChild variant="ghost">
              <a href={`${docs}/compliance/erasure`}>
                How erasure works
                <ExternalLink aria-hidden />
              </a>
            </Button>
          ) : null
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
        <EmptyState icon={<Eraser aria-hidden strokeWidth={1.75} />} title="Nothing to erase">
          The project has no site, so it holds no web data.
        </EmptyState>
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
  const today = utcToday(new Date());
  const domain = site?.domain ?? "the site";
  const text = confirmText(values.kind, domain);
  const kinds: { kind: ErasureKind; hint: string }[] = [
    {
      kind: "visitor_today",
      hint: `What one visitor did today, ${formatDate(today)} (UTC). You need their IP address and user agent.`,
    },
    {
      kind: "session",
      hint: "One session at any time while raw data is kept. You need its session id.",
    },
    {
      kind: "site_reset",
      hint: "All events and sessions of the site. The site keeps collecting new visits.",
    },
  ];

  return (
    <section aria-labelledby="erasure-h" className="flex min-w-0 flex-col gap-4">
      <h2 id="erasure-h" className="text-title text-fg">
        Request erasure
      </h2>
      <form
        noValidate
        autoComplete="off"
        onSubmit={submit}
        aria-label="Request erasure"
        className="max-w-prose flex min-w-0 flex-col gap-6"
      >
        <div className="max-w-sm">
          <SelectField
            label="Site"
            name="site"
            className="w-full max-lg:h-(--vs-control-touch)"
            value={values.siteId}
            // a session id belongs to one site; a chosen one does not carry over
            onChange={(e) => change({ siteId: e.target.value, sessionId: "" })}
            error={errors.siteId}
          >
            {sites.length > 1 ? <option value="">Choose a site</option> : null}
            {sites.map((s) => (
              <option key={s.id} value={s.id}>
                {s.name} ({s.domain})
              </option>
            ))}
          </SelectField>
        </div>

        <fieldset className="flex min-w-0 flex-col gap-2">
          <legend className="pb-2 text-small font-medium text-fg">What to erase</legend>
          <div className="grid gap-2 md:grid-cols-3">
            {kinds.map((k) => (
              <ChoiceCard
                key={k.kind}
                name="erasure-kind"
                value={k.kind}
                checked={values.kind === k.kind}
                onChange={() => change({ kind: k.kind })}
                title={KIND_LABEL[k.kind]}
                description={k.hint}
              />
            ))}
          </div>
        </fieldset>

        {/* the fields of the chosen kind fade in; no exit wait, so they are
            there at once for the keyboard */}
        <motion.div
          key={values.kind}
          initial={fade.initial}
          animate={fade.animate}
          className="flex min-w-0 flex-col gap-4 empty:hidden"
        >
          {values.kind === "visitor_today" ? (
            <>
              <TextField
                label="Visitor's IP address"
                name="erasure-ip"
                autoComplete="off"
                autoCapitalize="none"
                spellCheck={false}
                inputMode="text"
                className="font-mono max-lg:h-(--vs-control-touch)"
                placeholder="203.0.113.7"
                hint="IPv4 or IPv6, as the visitor's browser used it today."
                value={values.ip}
                onChange={(e) => change({ ip: e.target.value })}
                error={errors.ip}
              />
              <TextAreaField
                label="Visitor's user agent"
                name="erasure-user-agent"
                rows={3}
                className="font-mono text-small"
                autoComplete="off"
                autoCapitalize="none"
                spellCheck={false}
                placeholder="Mozilla/5.0 (X11; Linux x86_64) ..."
                hint="Exactly as the browser sends it. The service and this page do not store either value."
                value={values.userAgent}
                onChange={(e) => change({ userAgent: e.target.value })}
                error={errors.userAgent}
              />
            </>
          ) : null}

          {values.kind === "session" && site ? (
            <SessionFinder
              tenantId={site.tenant_id}
              site={site}
              chosen={values.sessionId}
              onChoose={(id) => change({ sessionId: id })}
            />
          ) : null}
          {values.kind === "session" ? (
            <TextField
              label="Session id"
              name="erasure-session"
              autoComplete="off"
              autoCapitalize="none"
              spellCheck={false}
              className="font-mono max-lg:h-(--vs-control-touch)"
              placeholder="2f1c7a9e-5b3d-4c8e-9a1f-6d0b2e4c8a7f"
              hint="Choose the session in the list above, or paste its id."
              value={values.sessionId}
              onChange={(e) => change({ sessionId: e.target.value })}
              error={errors.sessionId}
            />
          ) : null}
        </motion.div>

        <div className="flex flex-wrap items-center gap-3">
          <Button
            type="submit"
            variant="destructive-outline"
            className="max-lg:h-(--vs-control-touch)"
          >
            <Eraser aria-hidden />
            {values.kind === "site_reset" ? "Reset site data" : "Request erasure"}
          </Button>
        </div>
        {confirming || fieldError ? null : special ? (
          <FormMessage error={new Error(special)} />
        ) : (
          <FormMessage error={send.error} />
        )}
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
        <Bone className="h-16 w-full" />
      </div>
    );
  const r = q.data;
  const summary = requestSummary(r);
  const tone = LOOK[summary]?.tone ?? "info";
  return (
    <section
      aria-label="Latest erasure request"
      aria-live="polite"
      className={cn(
        "flex animate-rise-in items-start gap-3 rounded-lg border bg-raised p-4 text-body",
        r.status === "failed"
          ? "border-critical-line"
          : r.status === "done"
            ? "border-success-line"
            : "border-line-strong",
      )}
    >
      <StatusDot tone={tone} live={summary === "Running"} className="mt-2" />
      <div className="flex min-w-0 flex-col gap-1">
        <p className="font-medium text-fg">
          {`${KIND_LABEL[r.kind]} on ${site?.domain ?? "the site"}: ${summary.toLowerCase()}`}
        </p>
        <p className="text-small text-fg-2">
          {r.status === "running"
            ? "The request waits until the queued events of the site are stored, then deletes. This usually takes seconds."
            : (r.message ?? r.error ?? "")}
        </p>
      </div>
    </section>
  );
}

const LOOK: Record<string, { tone: Tone; icon: ReactNode; text: string }> = {
  Running: { tone: "info", icon: null, text: "text-fg" },
  Erased: { tone: "success", icon: <Check aria-hidden />, text: "text-fg" },
  "Nothing found": { tone: "neutral", icon: <Minus aria-hidden />, text: "text-fg-2" },
  Failed: { tone: "critical", icon: <X aria-hidden />, text: "text-fg" },
};

// the status of a request as an icon and a word
function SummaryCell({ r }: { r: ErasureRequest }) {
  const summary = requestSummary(r);
  const look = LOOK[summary];
  return (
    <span
      className={cn(
        "flex items-center gap-2 whitespace-nowrap font-medium [&_svg]:size-4",
        look?.text ?? "text-fg-2",
      )}
    >
      {summary === "Running" ? (
        <StatusDot tone="info" live />
      ) : (
        <span
          className={cn(
            summary === "Erased"
              ? "text-success"
              : summary === "Failed"
                ? "text-critical"
                : "text-fg-2",
          )}
        >
          {look?.icon}
        </span>
      )}
      {summary}
    </span>
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

function target(r: ErasureRequest): string {
  if (r.kind === "visitor_today")
    return `Visitor of ${r.day ? formatDate(r.day) : "the day"} (UTC)`;
  if (r.kind === "session") return `Session ${r.session_id ?? ""}`;
  return "All web data";
}

function History({ tenantId, sites }: { tenantId: string; sites: Site[] }) {
  const list = useInfiniteQuery(erasureListQuery(tenantId));
  const names = new Map(sites.map((s) => [s.id, s.domain]));
  const rows = list.data?.pages.flatMap((p) => p.items) ?? [];
  const columns: Column<ErasureRequest>[] = [
    {
      key: "request",
      header: "Request",
      primary: true,
      minWidth: "min-w-64",
      cell: (r) => (
        <span className="flex min-w-0 flex-col gap-1 py-2">
          <span className="font-medium text-fg">
            {KIND_LABEL[r.kind]}: {names.get(r.site_id) ?? "a deleted site"}
          </span>
          <span className="break-all font-normal text-fg-2">{target(r)}</span>
        </span>
      ),
    },
    {
      key: "status",
      header: "Status",
      mobile: true,
      cell: (r) => <SummaryCell r={r} />,
    },
    {
      key: "requested",
      header: "Requested",
      mobile: true,
      cell: (r) => (
        <span className="whitespace-nowrap text-fg-2">{formatDateTimeUtc(r.requested_at)}</span>
      ),
    },
    {
      key: "result",
      header: "Result",
      minWidth: "min-w-64",
      cell: (r) => (
        <span className="flex flex-col gap-1 py-2 text-fg-2">
          {r.message ? <span>{r.message}</span> : null}
          {r.error ? <span className="text-critical">{r.error}</span> : null}
          {deletedText(r) ? <span>{deletedText(r)}</span> : null}
          {r.completed_at ? <span>Finished {formatDateTimeUtc(r.completed_at)}.</span> : null}
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
        rowLabel={(r) => `${KIND_LABEL[r.kind]} ${r.id}`}
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
    <fieldset className="flex flex-col gap-3 rounded-md border border-line p-3">
      <legend className="px-1 text-small font-medium text-fg">Find the session</legend>
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
          <Bone className="h-8 w-full" />
          <Bone className="h-8 w-full" />
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
          <ul aria-label="Sessions" className="flex max-h-72 flex-col gap-1 overflow-y-auto">
            {q.data.items.map((x) => (
              <li key={x.id}>
                <button
                  type="button"
                  aria-pressed={chosen === x.id}
                  onClick={() => onChoose(x.id)}
                  className={cn(
                    "flex w-full flex-wrap items-baseline gap-x-3 gap-y-1 rounded-sm px-2 py-1 text-left text-small transition-colors duration-(--vs-duration-press) hover:bg-hover focus-ring",
                    chosen === x.id && "bg-selected",
                  )}
                >
                  <span className="tabular text-fg">{formatTimeRange(x.first_at, x.last_at)}</span>
                  <span className="text-fg-2">
                    {formatCount(x.views)} {x.views === 1 ? "page view" : "page views"},{" "}
                    {[
                      x.browser && keyLabel("browser", x.browser),
                      x.os,
                      x.device && keyLabel("device", x.device),
                      x.country && keyLabel("country", x.country),
                    ]
                      .filter(Boolean)
                      .join(", ")}
                  </span>
                  <span className="font-mono text-caption text-fg-3">{x.id}</span>
                </button>
              </li>
            ))}
          </ul>
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
    </fieldset>
  );
}
