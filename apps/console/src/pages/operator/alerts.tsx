import { useQuery } from "@tanstack/react-query";
import { CircleAlert, CircleCheck, ExternalLink, RotateCcw, TriangleAlert } from "lucide-react";
import { useEffect, useState } from "react";
import { ApiError } from "@/api/client";
import { ALERTS_REFRESH_MS, operatorAlertsQuery } from "@/api/queries";
import type { AlertSource, FiringAlert, OperatorAlerts } from "@/api/schemas";
import { ErrorPanel, Loading } from "@/components/error-panel";
import { Button } from "@/components/ui/button";
import {
  firingFor,
  formatClockUtc,
  groupBySeverity,
  runbookUrl,
  type SeverityGroup,
} from "@/lib/alerts";
import { formatDateTimeUtc } from "@/lib/format";

const WATCHDOG_RUNBOOK = "docs/runbooks/alerts.md#watchdog";

// re-renders on an interval, so "firing for" moves between refreshes too
function useNow(everyMs: number): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), everyMs);
    return () => clearInterval(t);
  }, [everyMs]);
  return now;
}

// contract 11: the live alerts of alertmanager. this page is where alerts
// show when no push channel an operator input is configured.
export function AlertsPage() {
  const alerts = useQuery(operatorAlertsQuery);
  const now = useNow(15_000);

  return (
    <section className="space-y-5">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="text-xl font-semibold tracking-tight">Alerts</h2>
          <p className="mt-1 max-w-2xl text-sm text-muted-foreground">
            What Alertmanager holds right now, without silenced alerts. The page refreshes every{" "}
            {ALERTS_REFRESH_MS / 1000} seconds. Alerts reach nobody else unless a push channel is
            configured, so keep an eye on this page.
          </p>
        </div>
        <Button
          variant="outline"
          size="sm"
          onClick={() => alerts.refetch()}
          disabled={alerts.isFetching}
        >
          <RotateCcw aria-hidden />
          {alerts.isFetching ? "Checking" : "Check now"}
        </Button>
      </div>

      {alerts.isPending ? (
        <Loading label="Loading the alerts" />
      ) : alerts.error ? (
        <AlertsError error={alerts.error} lastSuccess={alerts.dataUpdatedAt} />
      ) : (
        <AlertsView data={alerts.data} now={now} />
      )}
    </section>
  );
}

function AlertsError({ error, lastSuccess }: { error: unknown; lastSuccess: number }) {
  const unreachable = error instanceof ApiError && error.code === "alert_source_unreachable";
  const unconfigured = error instanceof ApiError && error.code === "alert_source_not_configured";
  return (
    <div className="space-y-2">
      <ErrorPanel
        error={error}
        title={
          unreachable
            ? "Alert source unreachable"
            : unconfigured
              ? "No alert source configured"
              : "The alerts could not be loaded"
        }
      />
      <p className="text-sm text-muted-foreground">
        {unreachable || unconfigured
          ? "No alert list is shown: without an answer from Alertmanager there is no way to tell whether anything fires. "
          : null}
        {lastSuccess > 0
          ? `Last successful check ${formatClockUtc(new Date(lastSuccess).toISOString())}. `
          : null}
        The page keeps retrying every {ALERTS_REFRESH_MS / 1000} seconds.
      </p>
    </div>
  );
}

function AlertsView({ data, now }: { data: OperatorAlerts; now: number }) {
  const down = data.sources.filter((s) => !s.reachable);
  const groups = groupBySeverity(data.alerts);
  return (
    <div className="space-y-5">
      <p className="text-sm text-muted-foreground">
        Last checked <time dateTime={data.checked_at}>{formatClockUtc(data.checked_at)}</time>
      </p>
      {data.watchdog_firing ? null : <PipelineDown />}
      {down.length > 0 ? <SourcesDown down={down} total={data.sources.length} /> : null}
      {data.alerts.length === 0 ? (
        data.watchdog_firing ? (
          <AllClear checkedAt={data.checked_at} />
        ) : (
          <p className="rounded-xl border border-dashed bg-card p-6 text-sm text-muted-foreground">
            Alertmanager holds no alert, but this is not all clear: the pipeline that delivers
            alerts to it is down.
          </p>
        )
      ) : (
        groups.map((g) => <SeverityList key={g.key} group={g} now={now} />)
      )}
    </div>
  );
}

function AllClear({ checkedAt }: { checkedAt: string }) {
  return (
    <div className="flex items-start gap-3 rounded-xl border bg-card p-6">
      <CircleCheck aria-hidden className="mt-0.5 size-5 shrink-0 text-good" />
      <div>
        <p className="font-medium">All clear</p>
        <p className="mt-1 text-sm text-muted-foreground">
          No alert is firing. Last checked{" "}
          <time dateTime={checkedAt}>{formatClockUtc(checkedAt)}</time>.
        </p>
      </div>
    </div>
  );
}

// the watchdog always fires; without it prometheus is not evaluating rules
// or cannot reach alertmanager, so alerts can be missing from this page
function PipelineDown() {
  const runbook = runbookUrl(WATCHDOG_RUNBOOK);
  return (
    <div
      role="alert"
      className="flex items-start gap-3 rounded-lg border border-destructive/40 bg-destructive/5 p-4 text-sm"
    >
      <CircleAlert aria-hidden className="mt-0.5 size-4 shrink-0 text-destructive" />
      <div>
        <p className="font-medium text-destructive">Alerting pipeline down</p>
        <p className="mt-1 text-muted-foreground">
          The always-firing Watchdog alert is missing from Alertmanager. Prometheus is not
          evaluating the alert rules or cannot reach Alertmanager, so alerts that should fire may
          not be listed here.{" "}
          {runbook ? (
            <a href={runbook} target="_blank" rel="noreferrer" className="underline">
              Watchdog runbook
            </a>
          ) : null}
        </p>
      </div>
    </div>
  );
}

function SourcesDown({ down, total }: { down: AlertSource[]; total: number }) {
  return (
    <div
      role="status"
      className="flex items-start gap-3 rounded-lg border border-warn/40 bg-warn/5 p-4 text-sm"
    >
      <TriangleAlert aria-hidden className="mt-0.5 size-4 shrink-0 text-warn" />
      <div>
        <p className="font-medium">
          {down.length} of {total} Alertmanager members did not answer
        </p>
        <p className="mt-1 text-muted-foreground">
          The list comes from the members that answered; each member holds every alert.
        </p>
        <ul className="mt-2 space-y-1 font-mono text-xs">
          {down.map((s) => (
            <li key={s.address}>
              {s.address}: {s.error}
            </li>
          ))}
        </ul>
      </div>
    </div>
  );
}

function SeverityList({ group, now }: { group: SeverityGroup; now: number }) {
  const headingId = `severity-${group.key || "none"}`;
  return (
    <section aria-labelledby={headingId} className="space-y-2">
      <h3 id={headingId} className="text-sm font-semibold">
        {group.label}
        {group.hint ? (
          <span className="font-normal text-muted-foreground">, {group.hint}</span>
        ) : null}{" "}
        <span className="font-normal text-muted-foreground">({group.alerts.length})</span>
      </h3>
      <ul className="divide-y rounded-xl border bg-card">
        {group.alerts.map((a) => (
          <AlertRow key={a.fingerprint} alert={a} now={now} />
        ))}
      </ul>
    </section>
  );
}

function AlertRow({ alert, now }: { alert: FiringAlert; now: number }) {
  const runbook = runbookUrl(alert.runbook);
  const where = [
    alert.node ? ["Node", alert.node] : null,
    alert.service ? ["Service", alert.service] : null,
    alert.instance ? ["Instance", alert.instance] : null,
  ].filter((x): x is [string, string] => x !== null);
  return (
    <li className="space-y-1.5 p-4" data-alert={alert.name}>
      <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
        <p className="font-medium">
          {runbook ? (
            <a
              href={runbook}
              target="_blank"
              rel="noreferrer"
              className="inline-flex items-center gap-1 hover:underline"
            >
              {alert.name}
              <ExternalLink aria-hidden className="size-3.5 text-muted-foreground" />
              <span className="sr-only">(runbook)</span>
            </a>
          ) : (
            alert.name
          )}
        </p>
        <p className="text-sm text-muted-foreground">
          Firing for <span className="text-foreground">{firingFor(alert.starts_at, now)}</span>,
          since <time dateTime={alert.starts_at}>{formatDateTimeUtc(alert.starts_at)}</time>
        </p>
      </div>
      {alert.summary ? <p className="text-sm">{alert.summary}</p> : null}
      {alert.description ? (
        <p className="text-sm text-muted-foreground">{alert.description}</p>
      ) : null}
      {where.length > 0 ? (
        <dl className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
          {where.map(([k, v]) => (
            <div key={k} className="flex gap-1">
              <dt>{k}</dt>
              <dd className="font-mono text-foreground">{v}</dd>
            </div>
          ))}
        </dl>
      ) : null}
      {alert.state === "suppressed" ? (
        <p className="text-xs text-muted-foreground">
          {alert.inhibited_by.length > 0
            ? "Inhibited: another firing alert explains this one, so it sends no notification."
            : "Suppressed: Alertmanager holds back its notifications."}
        </p>
      ) : alert.state === "unprocessed" ? (
        <p className="text-xs text-muted-foreground">
          Just received: Alertmanager has not processed it yet.
        </p>
      ) : null}
    </li>
  );
}
