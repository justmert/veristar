import { type UseQueryResult, useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import {
  BookOpen,
  CircleAlert,
  Clock,
  Info,
  RotateCcw,
  ShieldCheck,
  TriangleAlert,
  WifiOff,
} from "lucide-react";
import { useEffect, useState } from "react";
import { ApiError } from "@/api/client";
import { ALERTS_REFRESH_MS, operatorAlertsQuery } from "@/api/queries";
import type { AlertSource, FiringAlert, OperatorAlerts } from "@/api/schemas";
import { ErrorPanel, Loading } from "@/components/error-panel";
import { Stagger, StaggerItem } from "@/components/motion";
import { Notice } from "@/components/notice";
import { StatusDot } from "@/components/status";
import { Button } from "@/components/ui/button";
import {
  type AlertTone,
  alertUrgency,
  firingFor,
  formatClockUtc,
  groupBySeverity,
  runbookUrl,
  type SeverityGroup,
} from "@/lib/alerts";
import { errorText } from "@/lib/errors";
import { formatDateTimeUtc } from "@/lib/format";

const WATCHDOG_RUNBOOK = "docs/runbooks/alerts.md#watchdog";

// re-renders on an interval, so "firing for" moves between refreshes too
export function useNow(everyMs: number): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), everyMs);
    return () => clearInterval(t);
  }, [everyMs]);
  return now;
}

// contract 11: the live alerts of alertmanager. this page is where alerts
// show when no push channel (the optional discord webhook) is configured.
export function AlertsPage() {
  const alerts = useQuery(operatorAlertsQuery);
  const now = useNow(15_000);
  const count = alerts.error ? null : (alerts.data?.alerts.length ?? null);
  const refreshing = alerts.isFetching && !alerts.isPending;

  return (
    <section aria-labelledby="alerts-h" className="flex flex-col gap-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex min-w-0 flex-col gap-1">
          <h2 id="alerts-h" className="flex items-baseline gap-2 text-section text-fg">
            Alerts
            {count !== null ? (
              <span className="font-mono text-body font-normal text-fg-3">{count}</span>
            ) : null}
          </h2>
          <p className="max-w-prose text-small text-fg-2">
            What Alertmanager holds right now, without silenced alerts. The page refreshes every{" "}
            {ALERTS_REFRESH_MS / 1000} seconds. Alerts reach nobody else unless a push channel is
            configured, so keep an eye on this page.
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-3">
          <LiveCheck alerts={alerts} />
          <Button
            variant="outline"
            size="sm"
            className="max-lg:h-(--vs-control-touch)"
            onClick={() => alerts.refetch()}
            pending={refreshing}
          >
            {refreshing ? null : <RotateCcw aria-hidden />}
            {alerts.isFetching ? "Checking" : "Check now"}
          </Button>
        </div>
      </div>

      {alerts.isPending ? (
        <Loading label="Loading the alerts" />
      ) : alerts.error ? (
        <AlertsError
          error={alerts.error}
          lastSuccess={alerts.dataUpdatedAt}
          onRetry={() => alerts.refetch()}
        />
      ) : (
        <AlertsView data={alerts.data} now={now} />
      )}
    </section>
  );
}

// the breathing dot says the page is listening; it stops when the source
// does not answer, because then nothing is being heard
export function LiveCheck({ alerts }: { alerts: UseQueryResult<OperatorAlerts> }) {
  if (alerts.isPending) return null;
  if (alerts.error)
    return (
      <span className="flex items-center gap-1.5 text-caption text-fg-3">
        <StatusDot tone="critical" />
        {alerts.error instanceof ApiError && alerts.error.code === "alert_source_not_configured"
          ? "No alert source"
          : "Not answering"}
      </span>
    );
  return (
    <span className="flex items-center gap-1.5 text-caption text-fg-2">
      <StatusDot tone="success" live />
      <span>
        Last checked{" "}
        <time dateTime={alerts.data.checked_at}>{formatClockUtc(alerts.data.checked_at)}</time>
      </span>
    </span>
  );
}

export function AlertsError({
  error,
  lastSuccess,
  onRetry,
}: {
  error: unknown;
  lastSuccess: number;
  onRetry: () => void;
}) {
  const unreachable = error instanceof ApiError && error.code === "alert_source_unreachable";
  const unconfigured = error instanceof ApiError && error.code === "alert_source_not_configured";
  if (!unreachable && !unconfigured) {
    return (
      <div className="flex flex-col gap-2">
        <ErrorPanel error={error} title="The alerts could not be loaded" onRetry={onRetry} />
        <RetryNote lastSuccess={lastSuccess} />
      </div>
    );
  }
  return (
    <Notice
      tone={unreachable ? "warning" : "info"}
      role={unreachable ? "alert" : "status"}
      icon={unreachable ? <WifiOff aria-hidden /> : <Info aria-hidden />}
      title={unreachable ? "Alert source unreachable" : "No alert source configured"}
      actions={
        unreachable ? (
          <Button variant="outline" size="sm" onClick={onRetry}>
            <RotateCcw aria-hidden />
            Retry
          </Button>
        ) : null
      }
    >
      <span className="flex flex-col gap-1">
        <span>
          No alert list is shown: without an answer from Alertmanager there is no way to tell
          whether anything fires. {errorText(error)}
        </span>
        <RetryNote lastSuccess={lastSuccess} />
      </span>
    </Notice>
  );
}

function RetryNote({ lastSuccess }: { lastSuccess: number }) {
  return (
    <span className="text-caption text-fg-3">
      {lastSuccess > 0
        ? `Last successful check ${formatClockUtc(new Date(lastSuccess).toISOString())}. `
        : null}
      The page keeps retrying every {ALERTS_REFRESH_MS / 1000} seconds.
    </span>
  );
}

function AlertsView({ data, now }: { data: OperatorAlerts; now: number }) {
  const down = data.sources.filter((s) => !s.reachable);
  const groups = groupBySeverity(data.alerts);
  return (
    <div className="flex flex-col gap-5">
      {data.watchdog_firing ? null : <PipelineDown />}
      {down.length > 0 ? <SourcesDown down={down} total={data.sources.length} /> : null}
      {data.alerts.length === 0 ? (
        data.watchdog_firing ? (
          <AllClear checkedAt={data.checked_at} />
        ) : (
          <p className="rounded-lg border border-dashed border-line-strong px-4 py-6 text-small text-fg-2">
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
    <Notice tone="success" icon={<ShieldCheck aria-hidden />} title="All clear">
      <span className="flex flex-col gap-1">
        <span>
          No alert is firing. Last checked{" "}
          <time dateTime={checkedAt}>{formatClockUtc(checkedAt)}</time>.
        </span>
        <span className="text-caption text-fg-3">
          Alertmanager answered, holds no alert, and the always-firing Watchdog alert is present, so
          the pipeline works.
        </span>
      </span>
    </Notice>
  );
}

// the watchdog always fires; without it prometheus is not evaluating rules
// or cannot reach alertmanager, so alerts can be missing from this page
export function PipelineDown() {
  const runbook = runbookUrl(WATCHDOG_RUNBOOK);
  return (
    <Notice
      tone="critical"
      role="alert"
      icon={<TriangleAlert aria-hidden />}
      title="Alerting pipeline down"
      actions={
        runbook ? (
          <Button asChild variant="outline" size="sm">
            <a href={runbook} target="_blank" rel="noreferrer">
              <BookOpen aria-hidden />
              Watchdog runbook
            </a>
          </Button>
        ) : null
      }
    >
      The always-firing Watchdog alert is missing from Alertmanager. Prometheus is not evaluating
      the alert rules or cannot reach Alertmanager, so this list can miss alerts that fire. Treat it
      as incomplete until the Watchdog is back.
    </Notice>
  );
}

function SourcesDown({ down, total }: { down: AlertSource[]; total: number }) {
  return (
    <Notice tone="warning" title={`${down.length} of ${total} Alertmanager members did not answer`}>
      <span className="flex flex-col gap-2">
        <span>The list comes from the members that answered; each member holds every alert.</span>
        <ul className="flex flex-col gap-1 font-mono text-caption text-fg-2">
          {down.map((s) => (
            <li key={s.address} className="break-all">
              {s.address}: {s.error}
            </li>
          ))}
        </ul>
      </span>
    </Notice>
  );
}

function SeverityList({ group, now }: { group: SeverityGroup; now: number }) {
  const headingId = `severity-${group.key || "none"}`;
  return (
    <section aria-labelledby={headingId} className="flex flex-col gap-2">
      <h3 id={headingId} className="text-title text-fg">
        {group.label}
        {group.hint ? <span className="font-normal text-fg-2">, {group.hint}</span> : null}{" "}
        <span className="font-mono text-small font-normal text-fg-3">({group.alerts.length})</span>
      </h3>
      <Stagger as="ul" className="flex flex-col gap-2">
        {group.alerts.map((a) => (
          <StaggerItem as="li" key={a.fingerprint}>
            <AlertItem alert={a} now={now} />
          </StaggerItem>
        ))}
      </Stagger>
    </section>
  );
}

const TONE_TEXT: Record<AlertTone, string> = {
  critical: "text-critical",
  warning: "text-warning",
  muted: "text-fg-2",
};

const TONE_ICON: Record<AlertTone, typeof CircleAlert> = {
  critical: TriangleAlert,
  warning: CircleAlert,
  muted: Clock,
};

// one alert (system v2 C-AlertItem): what is wrong in words first, its
// urgency, what it affects, the labels that place it, how long it fires and
// the runbook as the action
export function AlertItem({ alert, now }: { alert: FiringAlert; now: number }) {
  const runbook = runbookUrl(alert.runbook);
  const urgency = alertUrgency(alert);
  const Icon = TONE_ICON[urgency.tone];
  const where = [
    alert.node ? ["Node", alert.node] : null,
    alert.service ? ["Service", alert.service] : null,
    alert.instance ? ["Instance", alert.instance] : null,
  ].filter((x): x is [string, string] => x !== null);
  const held = alert.state === "suppressed";
  return (
    <article
      aria-label={alert.name}
      data-alert={alert.name}
      className={cn(
        "grid h-full grid-cols-[2rem_minmax(0,1fr)] items-start gap-x-3 gap-y-3 rounded-lg border border-line bg-raised px-4 py-3.5 transition-colors duration-(--vs-duration-press) ease-standard hover:border-line-strong sm:grid-cols-[2rem_minmax(0,1fr)_auto]",
        held && "opacity-70",
      )}
    >
      <span
        className={cn(
          "flex size-8 items-center justify-center rounded-md border border-line-strong",
          TONE_TEXT[urgency.tone],
        )}
      >
        <Icon aria-hidden className="size-4" />
      </span>
      <div className="flex min-w-0 flex-col gap-1">
        <p className="flex flex-wrap items-baseline gap-x-2.5 gap-y-1">
          <span className="text-body font-semibold text-fg">{alert.summary ?? alert.name}</span>
          <span className={cn("text-caption font-medium", TONE_TEXT[urgency.tone])}>
            {urgency.word}
          </span>
        </p>
        {alert.description ? <p className="text-small text-fg-2">{alert.description}</p> : null}
        {held ? (
          <p className="text-small text-fg-2">
            {alert.inhibited_by.length > 0
              ? "Inhibited: another firing alert explains this one, so it sends no notification."
              : "Suppressed: Alertmanager holds back its notifications."}
          </p>
        ) : alert.state === "unprocessed" ? (
          <p className="text-small text-fg-2">
            Just received: Alertmanager has not processed it yet.
          </p>
        ) : null}
        <dl className="flex flex-wrap gap-1.5 pt-0.5">
          <div className="flex rounded-sm border border-line px-1.5 font-mono text-axis text-fg-2">
            <dt className="sr-only">Rule</dt>
            <dd className="break-all">{alert.name}</dd>
          </div>
          {where.map(([k, v]) => (
            <div
              key={k}
              className="flex gap-1 rounded-sm border border-line px-1.5 font-mono text-axis"
            >
              <dt className="text-fg-3">{k}</dt>
              <dd className="break-all text-fg-2">{v}</dd>
            </div>
          ))}
        </dl>
      </div>
      <div className="col-span-2 flex flex-wrap items-center justify-between gap-2 sm:col-span-1 sm:flex-col sm:items-end">
        <span className="flex flex-col text-caption text-fg-2 sm:items-end">
          <span>
            Firing for <span className="font-mono text-fg">{firingFor(alert.starts_at, now)}</span>
          </span>
          <span className="text-fg-3">
            since <time dateTime={alert.starts_at}>{formatDateTimeUtc(alert.starts_at)}</time>
          </span>
        </span>
        {runbook ? (
          <Button asChild variant="outline" size="sm" className="max-lg:h-(--vs-control-touch)">
            <a href={runbook} target="_blank" rel="noreferrer">
              <BookOpen aria-hidden />
              Runbook<span className="sr-only"> for {alert.name}</span>
            </a>
          </Button>
        ) : null}
      </div>
    </article>
  );
}
