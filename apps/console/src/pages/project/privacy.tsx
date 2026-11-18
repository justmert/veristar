import { useInfiniteQuery, useMutation, useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { fade } from "@veristar/brand/motion";
import { Check, Clock, Eraser, Minus, X } from "lucide-react";
import { motion } from "motion/react";
import { type FormEvent, useEffect, useState } from "react";
import { ApiError } from "@/api/client";
import { api, type ErasureInput } from "@/api/endpoints";
import { erasureListQuery, erasureRequestQuery, keys, sitesQuery } from "@/api/queries";
import type { ErasureKind, ErasureRequest, Site } from "@/api/schemas";
import { type WebBand, webSessionsQuery } from "@/api/web";
import { ChoiceCard } from "@/components/choice-card";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { ErrorPanel, FormMessage, Loading, StateBlock } from "@/components/error-panel";
import { SelectField, TextAreaField, TextField } from "@/components/form-field";
import { Stagger, StaggerItem } from "@/components/motion";
import { Notice } from "@/components/notice";
import { PageHeader, Panel } from "@/components/panel";
import { DateField } from "@/components/range-control";
import { Segmented } from "@/components/segmented";
import { StatusDot, type Tone } from "@/components/status";
import { Button } from "@/components/ui/button";
import {
  confirmDomainField,
  ipField,
  KIND_LABEL,
  requestSummary,
  sessionField,
  userAgentField,
  utcToday,
} from "@/lib/erasure";
import { countOf, formatCount, formatDate, formatDateTimeUtc } from "@/lib/format";
import { siblingUrl } from "@/lib/hosts";
import { fieldMessage } from "@/lib/validate";
import { keyLabel } from "@/lib/web-dashboard";
import { useProject } from "@/pages/project-layout";
import { queryClient } from "@/query-client";

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
  confirmDomain: string;
};
export type ErasureErrors = Partial<
  Record<"siteId" | "ip" | "userAgent" | "sessionId" | "confirmDomain", string>
>;

const EMPTY = { ip: "", userAgent: "", sessionId: "", confirmDomain: "" };

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
      if (site) {
        const code = confirmDomainField(values.confirmDomain, site.domain);
        if (code === "invalid_value") errors.confirmDomain = `Type ${site.domain} exactly.`;
        else set("confirmDomain", code);
      }
      break;
  }
  return errors;
}

// the request body for the kind: only that kind's fields, trimmed. the
// gateway reads the user agent from a header, which http trims, so a pasted
// trailing space would otherwise compute another visitor's token.
export function erasureBody(values: ErasureValues): ErasureInput {
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
        confirm_domain: values.confirmDomain.trim(),
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

  return (
    <div className="flex flex-col gap-6">
      <PageHeader title="Privacy" />
      {sites.isPending ? (
        <Loading label="Loading the privacy page" />
      ) : sites.error ? (
        <ErrorPanel error={sites.error} onRetry={() => sites.refetch()} />
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
    <div className="grid animate-rise-in items-start gap-8 xl:grid-cols-[minmax(0,1fr)_20rem]">
      <div className="flex min-w-0 flex-col gap-6">
        <section aria-labelledby="privacy-h" className="flex flex-col gap-4">
          <h2 id="privacy-h" className="sr-only">
            Privacy
          </h2>
          <p className="max-w-prose text-body text-fg-2">
            Your project is the controller of its visitors' data. When a visitor asks you to erase
            their data, request it here. Every request is recorded below and in the audit log.
          </p>
          {live.length === 0 ? (
            <StateBlock
              kind="empty"
              icon={<Eraser aria-hidden strokeWidth={1.75} />}
              title="Nothing to erase"
            >
              This project has no sites, so it holds no web data to erase.
            </StateBlock>
          ) : isOwner ? (
            <ErasureForm tenantId={tenantId} sites={live} />
          ) : (
            <Notice tone="info" title="Owners request erasure">
              Only owners of this project can request erasure. You can follow every request in the
              history below.
            </Notice>
          )}
        </section>

        <History tenantId={tenantId} sites={all} />
      </div>

      <Explanation />
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

  const send = useMutation({
    mutationFn: (body: ErasureInput) => api.requestErasure(tenantId, body),
    onSuccess: async (r) => {
      setConfirming(false);
      setLastId(r.id);
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
      setErrors(serverErasureErrors(err));
      if (err instanceof ApiError && err.code === "validation_failed") setConfirming(false);
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
    if (values.kind === "site_reset") {
      setConfirming(true);
      return;
    }
    send.mutate(erasureBody(values));
  }

  const fieldError = Object.keys(serverErasureErrors(send.error)).length > 0;
  const special = erasureErrorText(send.error);
  const today = utcToday(new Date());
  const kinds: { kind: ErasureKind; hint: string }[] = [
    {
      kind: "visitor_today",
      hint: `Everything one visitor did on this site today, ${formatDate(`${today}T00:00:00Z`)} (UTC). You need the visitor's IP address and browser user agent.`,
    },
    {
      kind: "session",
      hint: "One session from the web dashboard, at any time during the retention period. You need its session id.",
    },
    {
      kind: "site_reset",
      hint: "Every event and session of this site, counted and quarantined, and its aggregates. The site keeps collecting new visits.",
    },
  ];

  return (
    <div className="flex flex-col gap-4">
      <form
        noValidate
        autoComplete="off"
        onSubmit={submit}
        aria-label="Request erasure"
        className="flex flex-col rounded-xl border border-line bg-raised"
      >
        <h3 className="border-b border-line px-4 py-3 text-title text-fg sm:px-5">
          Request erasure
        </h3>
        <div className="flex flex-col gap-5 p-4 sm:p-5">
          <div className="max-w-[24rem]">
            <SelectField
              label="Site"
              name="site"
              className="w-full max-lg:h-(--vs-control-touch)"
              value={values.siteId}
              // a session id belongs to one site; a chosen one does not carry over
              onChange={(e) => change({ siteId: e.target.value, confirmDomain: "", sessionId: "" })}
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

          <fieldset className="flex flex-col gap-2">
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
            className="flex flex-col gap-4"
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
                  hint="Exactly as the visitor's browser sends it. Both values are sent once to compute the visitor's token and are not stored by the service or by this page."
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

            {values.kind === "site_reset" ? (
              <TextField
                label="Type the site's domain to confirm"
                name="erasure-confirm-domain"
                autoComplete="off"
                autoCapitalize="none"
                spellCheck={false}
                className="font-mono max-lg:h-(--vs-control-touch)"
                placeholder={site?.domain ?? "example.org"}
                hint={
                  site
                    ? `All web data of ${site.domain} is deleted. This cannot be undone.`
                    : "Choose a site first."
                }
                value={values.confirmDomain}
                onChange={(e) => change({ confirmDomain: e.target.value })}
                error={errors.confirmDomain}
              />
            ) : null}
          </motion.div>

          <div className="flex flex-wrap items-center gap-3">
            <Button
              type="submit"
              variant={values.kind === "site_reset" ? "destructive" : "default"}
              pending={send.isPending}
              className="max-lg:h-(--vs-control-touch)"
            >
              {send.isPending ? null : <Eraser aria-hidden />}
              {send.isPending
                ? "Sending"
                : values.kind === "site_reset"
                  ? "Reset site data"
                  : "Request erasure"}
            </Button>
            <span className="text-small text-fg-3">Erasure cannot be undone.</span>
          </div>
          {confirming ? null : special ? (
            <p
              role="alert"
              className="flex animate-fade-in items-start gap-2 rounded-md border border-critical-line bg-critical-soft px-2 py-2 text-small text-fg"
            >
              <X aria-hidden className="mt-1 size-3.5 shrink-0 text-critical" />
              {special}
            </p>
          ) : (
            <FormMessage error={send.error && !fieldError ? send.error : null} />
          )}
        </div>
      </form>

      <ConfirmDialog
        open={confirming}
        onOpenChange={(o) => {
          setConfirming(o);
          if (!o) send.reset();
        }}
        title={`Reset all web data of ${site?.domain ?? "this site"}?`}
        description="Every event and session of this site is deleted, counted and quarantined traffic alike, together with its queued undeliverable events and its aggregates. The web dashboard starts again from zero for this site. This cannot be undone."
        cancelLabel="Keep the data"
        confirmLabel="Reset site data"
        pendingLabel="Resetting"
        pending={send.isPending}
        error={special ? new Error(special) : send.error}
        onConfirm={() => send.mutate(erasureBody(values))}
      />

      {lastId ? <LastRequest tenantId={tenantId} requestId={lastId} site={site} /> : null}
    </div>
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
  if (q.error) return <ErrorPanel error={q.error} onRetry={() => q.refetch()} />;
  if (!q.data) return <Loading label="Reading the request" rows={1} />;
  const r = q.data;
  const summary = requestSummary(r);
  const tone = SUMMARY[summary]?.tone ?? "info";
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
          {r.status === "running"
            ? `${KIND_LABEL[r.kind]} on ${site?.domain ?? "the site"}: running`
            : `${KIND_LABEL[r.kind]} on ${site?.domain ?? "the site"}: ${summary.toLowerCase()}`}
        </p>
        <p className="text-small text-fg-2">
          {r.status === "running"
            ? "The request waits until every event of this site that was queued before it has been stored, then deletes. This usually takes seconds."
            : (r.message ?? r.error ?? "")}
        </p>
      </div>
    </section>
  );
}

function History({ tenantId, sites }: { tenantId: string; sites: Site[] }) {
  const list = useInfiniteQuery(erasureListQuery(tenantId));
  const names = new Map(sites.map((s) => [s.id, s.domain]));
  return (
    <section
      aria-labelledby="erasure-history"
      className="flex flex-col rounded-xl border border-line bg-raised"
    >
      <h2
        id="erasure-history"
        className="border-b border-line px-4 py-3 text-title text-fg sm:px-5"
      >
        Request history
      </h2>
      {list.isPending ? (
        <Loading label="Loading the request history" className="px-4 sm:px-5" />
      ) : list.error ? (
        <div className="p-4">
          <ErrorPanel error={list.error} onRetry={() => list.refetch()} />
        </div>
      ) : (
        <>
          {list.data.pages[0]?.items.length === 0 ? (
            <p className="px-4 py-6 text-small text-fg-2 sm:px-5">No erasure was requested yet.</p>
          ) : (
            <Stagger as="ol" className="divide-y divide-line-subtle">
              {list.data.pages
                .flatMap((p) => p.items)
                .map((r) => (
                  <StaggerItem as="li" key={r.id}>
                    <HistoryRow r={r} domain={names.get(r.site_id) ?? "a deleted site"} />
                  </StaggerItem>
                ))}
            </Stagger>
          )}
          {list.hasNextPage ? (
            <div className="border-t border-line px-4 py-3 sm:px-5">
              <Button
                variant="outline"
                size="sm"
                onClick={() => list.fetchNextPage()}
                pending={list.isFetchingNextPage}
              >
                {list.isFetchingNextPage ? "Loading" : "Show older requests"}
              </Button>
            </div>
          ) : null}
        </>
      )}
    </section>
  );
}

const SUMMARY: Record<string, { tone: Tone; icon: React.ReactNode; text: string }> = {
  Running: { tone: "info", icon: <Clock aria-hidden />, text: "text-fg" },
  Erased: { tone: "success", icon: <Check aria-hidden />, text: "text-success" },
  "Nothing found": { tone: "info", icon: <Minus aria-hidden />, text: "text-fg-2" },
  Failed: { tone: "critical", icon: <X aria-hidden />, text: "text-critical" },
};

export function HistoryRow({ r, domain }: { r: ErasureRequest; domain: string }) {
  const summary = requestSummary(r);
  const look = SUMMARY[summary];
  const target =
    r.kind === "visitor_today"
      ? `visitor of ${r.day ? formatDate(`${r.day}T00:00:00Z`) : "the day"} (UTC)`
      : r.kind === "session"
        ? `session ${r.session_id ?? ""}`
        : "all web data";
  return (
    <div className="grid gap-x-4 gap-y-1 px-4 py-3 text-small sm:grid-cols-[7.5rem_minmax(0,1fr)] sm:px-5">
      <span
        className={cn(
          "flex items-center gap-1 font-medium [&_svg]:size-3.5",
          look?.text ?? "text-fg-2",
        )}
      >
        {summary === "Running" ? <StatusDot tone="info" live /> : look?.icon}
        {summary}
      </span>
      <div className="flex min-w-0 flex-col gap-1">
        <p className="font-medium text-fg">
          {KIND_LABEL[r.kind]}: {domain}
        </p>
        <p className="text-fg-3">
          <span className="break-all">{target}</span>, requested{" "}
          <span className="tabular">{formatDateTimeUtc(r.requested_at)}</span>
          {r.completed_at ? `, finished ${formatDateTimeUtc(r.completed_at)}` : ""}
        </p>
        {r.message ? <p className="text-fg-2">{r.message}</p> : null}
        {r.error ? <p className="text-critical">{r.error}</p> : null}
        {r.status !== "running" ? (
          <p className="text-fg-3">
            Deleted <span className="tabular">{countOf(r.sessions_deleted, "session")}</span>,{" "}
            <span className="tabular">{countOf(r.events_deleted, "event")}</span> and{" "}
            <span className="tabular">
              {countOf(r.dlq_messages_deleted, "undeliverable queued message")}
            </span>
            .
            {r.affected_from
              ? r.reaggregated_at
                ? ` Daily aggregates were recomputed ${formatDateTimeUtc(r.reaggregated_at)}.`
                : " Daily aggregates are being recomputed."
              : ""}
          </p>
        ) : null}
      </div>
    </div>
  );
}

function Explanation() {
  const docs = siblingUrl(window.location.hostname, "docs");
  return (
    <aside aria-labelledby="erasure-explained" className="flex min-w-0 flex-col gap-4">
      <Panel title={<span id="erasure-explained">What can and cannot be erased</span>}>
        <div className="flex flex-col gap-3 text-small text-fg-2">
          <p>
            The service never stores an IP address. A visitor is known only by a token computed from
            the IP address, the user agent, the site and a secret that exists in memory for one UTC
            day.
          </p>
          <dl className="flex flex-col gap-3">
            <div className="flex flex-col gap-1">
              <dt className="font-medium text-fg">One visitor, today</dt>
              <dd>
                Works only on the UTC day of the visit. On that day the service can compute the
                visitor's token again from the IP address and user agent you enter, and delete their
                sessions and events. At midnight UTC the day's secret is destroyed. After that
                nobody, including the operators, can tell which stored session belongs to which
                person, so a request for an earlier day finds nothing and says so.
              </dd>
            </div>
            <div className="flex flex-col gap-1">
              <dt className="font-medium text-fg">One session</dt>
              <dd>
                Works for as long as raw data is kept. If the visitor can point to a visit (for
                example by its day, time and browser), find it in the session list of this form and
                erase it by its id.
              </dd>
            </div>
            <div className="flex flex-col gap-1">
              <dt className="font-medium text-fg">Whole site</dt>
              <dd>
                Deletes every event and session of the site, counted and quarantined, and its
                aggregates, at any time. Use it when a visitor cannot be identified by either of the
                other two, or when personal data reached a page address, title or event by mistake.
              </dd>
            </div>
          </dl>
          <p>
            Every request first waits until events of the site that were queued before it are
            stored, so none of them reappear afterwards. Afterwards the affected days of the
            aggregate figures are recomputed. Data in backups stays there until the backups expire.
            API metrics hold no visitor data and are not affected.{" "}
            {docs ? (
              <>
                The full description is in the{" "}
                <a
                  href={`${docs}/compliance/erasure`}
                  className="rounded-sm text-fg underline underline-offset-2 focus-ring"
                >
                  erasure documentation
                </a>
                .
              </>
            ) : (
              "The full description is in the erasure documentation of the service."
            )}
          </p>
        </div>
      </Panel>
    </aside>
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
        <Loading label="Loading sessions" />
      ) : q.error ? (
        <ErrorPanel error={q.error} onRetry={() => q.refetch()} />
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
                  <span className="tabular text-fg">
                    {formatDateTimeUtc(x.first_at)} to {formatDateTimeUtc(x.last_at)}
                  </span>
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
