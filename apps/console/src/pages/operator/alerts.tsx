import { useQuery } from "@tanstack/react-query";
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
import { Notice } from "@/components/notice";
import { PageHeader } from "@/components/page-header";
import { ErrorState } from "@/components/states";
import { StatusDot } from "@/components/status";
import { Button } from "@/components/ui/button";
import {
  type AlertTone,
  alertUrgency,
  firingFor,
  groupBySeverity,
  groupTitle,
  runbookUrl,
  type SeverityGroup,
  sentenceCase,
} from "@/lib/alerts";
import { countOf, formatDateTimeUtc, formatTimeUtc } from "@/lib/format";
import { AlertsSkeleton } from "@/pages/operator/page-frame";

const WATCHDOG_RUNBOOK = "docs/runbooks/alerts.md#watchdog";
const EVERY = `${ALERTS_REFRESH_MS / 1000} seconds`;

// re-renders on an interval, so "firing for" moves between refreshes too
export function useNow(everyMs: number): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), everyMs);
    return () => clearInterval(t);
  }, [everyMs]);
  return now;
}

// contract 11: the alerts that fire now. this page is where they show when
// no push channel is set up, so it checks again on its own.
export function AlertsPage() {
  const alerts = useQuery(operatorAlertsQuery);
  const now = useNow(15_000);
  const count = alerts.data && !alerts.error ? alerts.data.alerts.length : null;
  const refreshing = alerts.isFetching && !alerts.isPending;

  return (
    <div className="flex min-w-0 flex-col gap-6">
      <PageHeader
        title="Alerts"
        meta={
          count !== null ? (
            <span className="text-body text-fg-2 tabular" data-count="">
              {count}
            </span>
          ) : null
        }
        description={`Alerts that fire now. The page checks again every ${EVERY}.`}
        secondary={
          <>
            <LiveState
              error={alerts.error}
              pending={alerts.isPending}
              checkedAt={alerts.data?.checked_at ?? null}
            />
            <Button
              variant="outline"
              className="max-lg:h-(--vs-control-touch)"
              onClick={() => alerts.refetch()}
              pending={refreshing}
            >
              {refreshing ? null : <RotateCcw aria-hidden />}
              {alerts.isFetching ? "Checking" : "Check now"}
            </Button>
          </>
        }
      />

      {alerts.isPending ? (
        <AlertsSkeleton />
      ) : alerts.error ? (
        <AlertsError
          error={alerts.error}
          lastSuccess={alerts.dataUpdatedAt}
          onRetry={() => alerts.refetch()}
        />
      ) : (
        <AlertsView data={alerts.data} now={now} />
      )}
    </div>
  );
}

// the breathing dot says the page is listening; it stops when the source
// does not answer, because then nothing is heard
function LiveState({
  error,
  pending,
  checkedAt,
}: {
  error: unknown;
  pending: boolean;
  checkedAt: string | null;
}) {
  if (pending) return null;
  if (error)
    return (
      <span className="flex items-center gap-1 text-caption text-fg-2">
        <StatusDot tone="critical" />
        {error instanceof ApiError && error.code === "alert_source_not_configured"
          ? "No alert source"
          : "Not answering"}
      </span>
    );
  return (
    <span className="flex items-center gap-1 text-caption text-fg-2">
      <StatusDot tone="success" live />
      {checkedAt ? (
        <span>
          Last checked{" "}
          <time dateTime={checkedAt}>{formatTimeUtc(checkedAt, { seconds: true })}</time>
        </span>
      ) : (
        "Live"
      )}
    </span>
  );
}

function AlertsError({
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
  if (!unreachable && !unconfigured)
    return (
      <div className="flex flex-col gap-2">
        <ErrorState title="The alerts did not load" error={error} onRetry={onRetry} />
        <RetryNote lastSuccess={lastSuccess} />
      </div>
    );
  return (
    <Notice
      tone={unreachable ? "warning" : "info"}
      role={unreachable ? "alert" : "status"}
      icon={unreachable ? <WifiOff aria-hidden /> : <Info aria-hidden />}
      title={unreachable ? "The alert service does not answer" : "No alert service is set up"}
      actions={
        unreachable ? (
          <Button variant="outline" size="sm" onClick={onRetry}>
            <RotateCcw aria-hidden />
            Try again
          </Button>
        ) : null
      }
    >
      <span className="flex flex-col gap-1">
        <span>
          {unreachable
            ? "Without an answer there is no list of alerts, so you cannot know if one fires. Check the alert service."
            : "Set up the alert service in the deploy settings. Until then no alert shows here."}
        </span>
        <RetryNote lastSuccess={lastSuccess} />
      </span>
    </Notice>
  );
}

function RetryNote({ lastSuccess }: { lastSuccess: number }) {
  return (
    <span className="text-caption text-fg-2">
      {lastSuccess > 0
        ? `Last answer ${formatTimeUtc(new Date(lastSuccess), { seconds: true })}. `
        : null}
      The page tries again every {EVERY}.
    </span>
  );
}

function AlertsView({ data, now }: { data: OperatorAlerts; now: number }) {
  const down = data.sources.filter((s) => !s.reachable);
  const groups = groupBySeverity(data.alerts);
  return (
    <div className="flex flex-col gap-6">
      {data.watchdog_firing ? null : <PipelineDown />}
      {down.length > 0 ? <SourcesDown down={down} total={data.sources.length} /> : null}
      {data.alerts.length === 0 ? (
        data.watchdog_firing ? (
          <Notice tone="success" icon={<ShieldCheck aria-hidden />} title="All clear">
            No alert fires. The alert checks run, so this list is complete.
          </Notice>
        ) : (
          <p className="rounded-lg border border-dashed border-line-strong px-4 py-6 text-small text-fg-2">
            No alert shows, but the alert checks do not run. An alert can fire without showing here.
          </p>
        )
      ) : (
        groups.map((g) => <SeverityList key={g.key} group={g} now={now} />)
      )}
    </div>
  );
}

// the always-on test alert is missing: the rules are not checked, or their
// results do not reach the alert service, so alerts can be missing here
function PipelineDown() {
  const runbook = runbookUrl(WATCHDOG_RUNBOOK);
  return (
    <Notice
      tone="critical"
      role="alert"
      icon={<TriangleAlert aria-hidden />}
      title="The alert checks do not run"
      actions={
        runbook ? (
          <Button asChild variant="outline" size="sm">
            <a href={runbook} target="_blank" rel="noreferrer">
              <BookOpen aria-hidden />
              Open the runbook
            </a>
          </Button>
        ) : null
      }
    >
      The always-on test alert is missing. An alert can fire without showing here. Treat this list
      as incomplete until the test alert is back.
    </Notice>
  );
}

function SourcesDown({ down, total }: { down: AlertSource[]; total: number }) {
  return (
    <Notice
      tone="warning"
      title={`${down.length} of ${countOf(total, "alert server")} did not answer`}
    >
      <span className="flex flex-col gap-2">
        <span>
          Each server holds every alert, so the list comes from the servers that answered.
        </span>
        <details className="text-caption">
          <summary className="w-fit cursor-pointer rounded-sm text-fg-2 hover:text-fg focus-ring">
            Servers and errors
          </summary>
          <ul className="mt-1 flex flex-col gap-1 font-mono text-fg-2">
            {down.map((s) => (
              <li key={s.address} className="break-all">
                {s.address}: {s.error}
              </li>
            ))}
          </ul>
        </details>
      </span>
    </Notice>
  );
}

function SeverityList({ group, now }: { group: SeverityGroup; now: number }) {
  const headingId = `severity-${group.key || "none"}`;
  return (
    <section aria-labelledby={headingId} className="flex flex-col gap-3" data-severity={group.key}>
      <h2 id={headingId} className="flex items-baseline gap-2 text-title text-fg">
        {groupTitle(group.key)}{" "}
        <span className="text-small font-normal text-fg-2 tabular">{group.alerts.length}</span>
      </h2>
      <ul className="flex flex-col gap-2">
        {group.alerts.map((a) => (
          <li key={a.fingerprint}>
            <AlertItem alert={a} now={now} />
          </li>
        ))}
      </ul>
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

const HELD_TEXT = {
  inhibited: "Held back: another alert that fires explains this one. It sends no message.",
  suppressed: "Held back: the alert service sends no message for it.",
  unprocessed: "Just received. The alert service did not process it yet.",
};

// one alert: what is wrong in words first, how long it fires, the runbook
// as the action, and the labels that place it as a secondary line
function AlertItem({ alert, now }: { alert: FiringAlert; now: number }) {
  const runbook = runbookUrl(alert.runbook);
  const urgency = alertUrgency(alert);
  const Icon = TONE_ICON[urgency.tone];
  const held = alert.state === "suppressed";
  const labels = [
    alert.name,
    alert.node ? `node ${alert.node}` : null,
    alert.service ? `service ${alert.service}` : null,
    alert.instance ? `instance ${alert.instance}` : null,
  ].filter((x): x is string => x !== null);
  return (
    <article
      aria-label={sentenceCase(alert.summary ?? alert.name)}
      data-alert={alert.name}
      className={cn(
        "grid min-w-0 grid-cols-[2rem_minmax(0,1fr)] items-start gap-x-3 gap-y-3 rounded-lg border border-line bg-raised p-4 sm:grid-cols-[2rem_minmax(0,1fr)_auto]",
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
        <span className="sr-only">{urgency.word}</span>
      </span>
      <div className="flex min-w-0 flex-col gap-1">
        <p className="text-body font-medium break-words text-fg">
          {sentenceCase(alert.summary ?? alert.name)}
        </p>
        <p className="text-small text-fg-2">
          Fires for <span className="text-fg tabular">{firingFor(alert.starts_at, now)}</span>,
          since <time dateTime={alert.starts_at}>{formatDateTimeUtc(alert.starts_at)}</time>
        </p>
        {alert.description ? (
          <p className="text-small break-words text-fg-2">{sentenceCase(alert.description)}</p>
        ) : null}
        {held ? (
          <p className="text-small text-fg-2">
            {alert.inhibited_by.length > 0 ? HELD_TEXT.inhibited : HELD_TEXT.suppressed}
          </p>
        ) : alert.state === "unprocessed" ? (
          <p className="text-small text-fg-2">{HELD_TEXT.unprocessed}</p>
        ) : null}
        <p className="font-mono text-caption break-all text-fg-2" data-labels="">
          {labels.join(" · ")}
        </p>
      </div>
      {runbook ? (
        <div className="col-span-2 sm:col-span-1">
          <Button asChild variant="outline" size="sm" className="max-lg:h-(--vs-control-touch)">
            <a href={runbook} target="_blank" rel="noreferrer">
              <BookOpen aria-hidden />
              Runbook<span className="sr-only"> for {alert.name}</span>
            </a>
          </Button>
        </div>
      ) : null}
    </article>
  );
}
