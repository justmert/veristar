import { useInfiniteQuery, useMutation, useQuery } from "@tanstack/react-query";
import { Eraser } from "lucide-react";
import { type FormEvent, useEffect, useState } from "react";
import { ApiError } from "@/api/client";
import { api, type ErasureInput } from "@/api/endpoints";
import { erasureListQuery, erasureRequestQuery, keys, sitesQuery } from "@/api/queries";
import type { ErasureKind, ErasureRequest, Site } from "@/api/schemas";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { ErrorPanel, FormMessage, Loading } from "@/components/error-panel";
import { SelectField, TextAreaField, TextField } from "@/components/form-field";
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
import { formatCount, formatDate, formatDateTimeUtc } from "@/lib/format";
import { fieldMessage } from "@/lib/validate";
import { queryClient } from "@/query-client";
import { useProject } from "./project-layout";

// the form's values. ip and userAgent are personal data the operator types on
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
      return "The dashboards of this site are still being set up, so it holds no web data yet. Try again in a minute.";
    case "unavailable":
      return "The ingestion service could not be reached, so nothing was erased. Try again shortly.";
    default:
      return null;
  }
}

export function PrivacyPage() {
  const { tenant, isOwner } = useProject();
  const sites = useQuery(sitesQuery(tenant.id));

  if (sites.isPending) return <Loading label="Loading the privacy page" />;
  if (sites.error) return <ErrorPanel error={sites.error} onRetry={() => sites.refetch()} />;
  const all = sites.data;
  const live = all.filter((s) => s.status !== "removed");

  return (
    <div className="space-y-10">
      <section className="space-y-4">
        <h2 className="text-xl font-semibold tracking-tight">Privacy</h2>
        <p className="max-w-prose text-sm text-muted-foreground">
          Your project is the controller of its visitors' data. When a visitor asks you to erase
          their data, request it here. Every request is recorded below and in the audit log.
        </p>
        {live.length === 0 ? (
          <p className="rounded-lg border bg-card p-4 text-sm text-muted-foreground">
            This project has no sites, so it holds no web data to erase.
          </p>
        ) : isOwner ? (
          <ErasureForm tenantId={tenant.id} sites={live} />
        ) : (
          <p className="rounded-lg border bg-card p-4 text-sm text-muted-foreground">
            Only owners of this project can request erasure. You can follow every request in the
            history below.
          </p>
        )}
      </section>

      <History tenantId={tenant.id} sites={all} />

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
    // the typed values stay in the form so the operator can correct them; like
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

  return (
    <div className="space-y-4">
      <form
        noValidate
        autoComplete="off"
        onSubmit={submit}
        aria-label="Request erasure"
        className="space-y-5 rounded-xl border bg-card p-5 sm:p-6"
      >
        <h3 className="font-semibold">Request erasure</h3>

        <SelectField
          label="Site"
          name="site"
          value={values.siteId}
          onChange={(e) => change({ siteId: e.target.value, confirmDomain: "" })}
          error={errors.siteId}
        >
          {sites.length > 1 ? <option value="">Choose a site</option> : null}
          {sites.map((s) => (
            <option key={s.id} value={s.id}>
              {s.name} ({s.domain})
            </option>
          ))}
        </SelectField>

        <fieldset className="space-y-2">
          <legend className="mb-2 text-sm font-medium">What to erase</legend>
          <KindOption
            kind="visitor_today"
            current={values.kind}
            onChange={(kind) => change({ kind })}
            hint={`Everything one visitor did on this site today, ${formatDate(`${today}T00:00:00Z`)} (UTC). You need the visitor's IP address and browser user agent.`}
          />
          <KindOption
            kind="session"
            current={values.kind}
            onChange={(kind) => change({ kind })}
            hint="One session from the web dashboard, at any time during the retention period. You need its session id."
          />
          <KindOption
            kind="site_reset"
            current={values.kind}
            onChange={(kind) => change({ kind })}
            hint="Every event and session of this site, in both dashboards, and its aggregates. The site keeps collecting new visits."
          />
        </fieldset>

        {values.kind === "visitor_today" ? (
          <div className="space-y-4">
            <TextField
              label="Visitor's IP address"
              name="erasure-ip"
              autoComplete="off"
              autoCapitalize="none"
              spellCheck={false}
              inputMode="text"
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
              className="font-mono text-xs"
              autoComplete="off"
              autoCapitalize="none"
              spellCheck={false}
              placeholder="Mozilla/5.0 (X11; Linux x86_64) ..."
              hint="Exactly as the visitor's browser sends it. Both values are sent once to compute the visitor's token and are not stored by the service or by this page."
              value={values.userAgent}
              onChange={(e) => change({ userAgent: e.target.value })}
              error={errors.userAgent}
            />
          </div>
        ) : null}

        {values.kind === "session" ? (
          <TextField
            label="Session id"
            name="erasure-session"
            autoComplete="off"
            autoCapitalize="none"
            spellCheck={false}
            className="font-mono"
            placeholder="2f1c7a9e-5b3d-4c8e-9a1f-6d0b2e4c8a7f"
            hint="Open the web dashboard, find the session on its Sessions page and copy the id from the page address."
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

        <div className="flex flex-wrap items-center gap-3">
          <Button
            type="submit"
            variant={values.kind === "site_reset" ? "destructive" : "default"}
            disabled={send.isPending}
          >
            <Eraser aria-hidden />
            {send.isPending
              ? "Sending"
              : values.kind === "site_reset"
                ? "Reset site data"
                : "Request erasure"}
          </Button>
          {confirming ? null : special ? (
            <p role="alert" className="text-sm text-destructive">
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
        description="Every event and session of this site is deleted from both web dashboards, together with its queued undeliverable events and its aggregates. Dashboards start again from zero. This cannot be undone."
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

function KindOption({
  kind,
  current,
  onChange,
  hint,
}: {
  kind: ErasureKind;
  current: ErasureKind;
  onChange: (k: ErasureKind) => void;
  hint: string;
}) {
  const id = `erasure-kind-${kind}`;
  return (
    <div className="flex items-start gap-3 rounded-lg border p-3 has-checked:border-primary has-checked:bg-primary/5">
      <input
        id={id}
        type="radio"
        name="erasure-kind"
        value={kind}
        checked={current === kind}
        onChange={() => onChange(kind)}
        aria-describedby={`${id}-hint`}
        className="mt-1 size-4 accent-primary"
      />
      <div>
        <label htmlFor={id} className="text-sm font-medium">
          {KIND_LABEL[kind]}
        </label>
        <p id={`${id}-hint`} className="text-sm text-muted-foreground">
          {hint}
        </p>
      </div>
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
  if (!q.data) return <Loading label="Reading the request" />;
  const r = q.data;
  return (
    <section
      aria-label="Latest erasure request"
      aria-live="polite"
      className={`rounded-lg border p-4 text-sm ${r.status === "failed" ? "border-destructive/40 bg-destructive/5" : r.status === "done" ? "border-good/40 bg-good/8" : "border-primary/30 bg-primary/5"}`}
    >
      <p className="font-medium">
        {r.status === "running"
          ? `${KIND_LABEL[r.kind]} on ${site?.domain ?? "the site"}: running`
          : `${KIND_LABEL[r.kind]} on ${site?.domain ?? "the site"}: ${requestSummary(r).toLowerCase()}`}
      </p>
      <p className="mt-1 text-muted-foreground">
        {r.status === "running"
          ? "The request waits until every event of this site that was queued before it has been stored, then deletes. This usually takes seconds."
          : (r.message ?? r.error ?? "")}
      </p>
    </section>
  );
}

function History({ tenantId, sites }: { tenantId: string; sites: Site[] }) {
  const list = useInfiniteQuery(erasureListQuery(tenantId));
  const names = new Map(sites.map((s) => [s.id, s.domain]));
  return (
    <section className="space-y-4" aria-labelledby="erasure-history">
      <h2 id="erasure-history" className="text-xl font-semibold tracking-tight">
        Request history
      </h2>
      {list.isPending ? (
        <Loading label="Loading the request history" />
      ) : list.error ? (
        <ErrorPanel error={list.error} onRetry={() => list.refetch()} />
      ) : (
        <>
          {list.data.pages[0]?.items.length === 0 ? (
            <p className="text-sm text-muted-foreground">No erasure was requested yet.</p>
          ) : (
            <ul className="divide-y overflow-hidden rounded-xl border bg-card">
              {list.data.pages
                .flatMap((p) => p.items)
                .map((r) => (
                  <li key={r.id}>
                    <HistoryRow r={r} domain={names.get(r.site_id) ?? "a deleted site"} />
                  </li>
                ))}
            </ul>
          )}
          {list.hasNextPage ? (
            <Button
              variant="outline"
              size="sm"
              onClick={() => list.fetchNextPage()}
              disabled={list.isFetchingNextPage}
            >
              {list.isFetchingNextPage ? "Loading" : "Show older requests"}
            </Button>
          ) : null}
        </>
      )}
    </section>
  );
}

const SUMMARY_TONE: Record<string, string> = {
  Running: "bg-primary/10 text-primary ring-primary/30",
  Erased: "bg-good/12 text-good ring-good/30",
  "Nothing found": "bg-muted text-muted-foreground ring-border",
  Failed: "bg-destructive/10 text-destructive ring-destructive/30",
};

export function HistoryRow({ r, domain }: { r: ErasureRequest; domain: string }) {
  const summary = requestSummary(r);
  const target =
    r.kind === "visitor_today"
      ? `visitor of ${r.day ? formatDate(`${r.day}T00:00:00Z`) : "the day"} (UTC)`
      : r.kind === "session"
        ? `session ${r.session_id ?? ""}`
        : "all web data";
  return (
    <div className="space-y-1 p-4 text-sm sm:p-5">
      <div className="flex flex-wrap items-center gap-2">
        <p className="font-medium">
          {KIND_LABEL[r.kind]}: {domain}
        </p>
        <span
          className={`inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset ${SUMMARY_TONE[summary] ?? ""}`}
        >
          <span aria-hidden className="size-1.5 rounded-full bg-current" />
          {summary}
        </span>
      </div>
      <p className="text-muted-foreground">
        <span className="break-all">{target}</span>, requested {formatDateTimeUtc(r.requested_at)}
        {r.completed_at ? `, finished ${formatDateTimeUtc(r.completed_at)}` : ""}
      </p>
      {r.message ? <p>{r.message}</p> : null}
      {r.error ? <p className="text-destructive">{r.error}</p> : null}
      {r.status !== "running" ? (
        <p className="text-muted-foreground">
          Deleted {formatCount(r.sessions_deleted)} session(s), {formatCount(r.events_deleted)}{" "}
          event(s) and {formatCount(r.dlq_messages_deleted)} undeliverable queued message(s).
          {r.affected_from
            ? r.reaggregated_at
              ? ` Daily aggregates were recomputed ${formatDateTimeUtc(r.reaggregated_at)}.`
              : " Daily aggregates are being recomputed."
            : ""}
        </p>
      ) : null}
    </div>
  );
}

function Explanation() {
  return (
    <section className="max-w-prose space-y-4 text-sm" aria-labelledby="erasure-explained">
      <h2 id="erasure-explained" className="text-xl font-semibold tracking-tight">
        What can and cannot be erased
      </h2>
      <p className="text-muted-foreground">
        The service never stores an IP address. A visitor is known only by a token computed from the
        IP address, the user agent, the site and a secret that exists in memory for one UTC day.
      </p>
      <dl className="space-y-3">
        <div>
          <dt className="font-medium">One visitor, today</dt>
          <dd className="text-muted-foreground">
            Works only on the UTC day of the visit. On that day the service can compute the
            visitor's token again from the IP address and user agent you enter, and delete their
            sessions and events. At midnight UTC the day's secret is destroyed. After that nobody,
            including the operators, can tell which stored session belongs to which person, so a
            request for an earlier day finds nothing and says so.
          </dd>
        </div>
        <div>
          <dt className="font-medium">One session</dt>
          <dd className="text-muted-foreground">
            Works for as long as raw data is kept. If the visitor can point to a visit (for example
            by its time and pages), find it on the dashboard's Sessions page and erase it by its id.
          </dd>
        </div>
        <div>
          <dt className="font-medium">Whole site</dt>
          <dd className="text-muted-foreground">
            Deletes every event and session of the site in both dashboards and its aggregates, at
            any time. Use it when a visitor cannot be identified by either of the other two, or when
            personal data reached a page address, title or event by mistake.
          </dd>
        </div>
      </dl>
      <p className="text-muted-foreground">
        Every request first waits until events of the site that were queued before it are stored, so
        none of them reappear afterwards. Afterwards the affected days of the aggregate figures are
        recomputed. Data in backups stays there until the backups expire. API metrics hold no
        visitor data and are not affected. The full description is in the service's erasure
        documentation (docs/compliance/erasure.md).
      </p>
    </section>
  );
}
