import { useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import {
  ArrowUpRight,
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
  criticalCount,
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
  const data = alerts.data && !alerts.error ? alerts.data : null;
  // the count beside the title is the count of the sidebar badge: every
  // firing alert
  const count = data ? data.alerts.length : null;
  const critical = data ? criticalCount(data.alerts) : 0;
  const refreshing = alerts.isFetching && !alerts.isPending;

  return (
    <div className="flex min-w-0 flex-col gap-6">
      <PageHeader
        title="Alerts"
        meta={
          count !== null && count > 0 ? (
            <span
              className={cn("text-[15px] tabular", critical > 0 ? "text-critical" : "text-fg-3")}
              data-count=""
            >
              {count}
            </span>
          ) : null
        }
        items={
          alerts.isPending
            ? []
            : [
                <SourceFact key="source" error={alerts.error} data={data} />,
                data ? (
                  <time
                    key="checked"
                    dateTime={new Date(data.checked_at).toISOString()}
                    title={`The page checks again every ${EVERY}.`}
                    data-freshness=""
                  >
                    checked {formatTimeUtc(data.checked_at)}
                  </time>
                ) : null,
                RUNBOOKS ? (
                  <a
                    key="runbooks"
                    href={RUNBOOKS}
                    target="_blank"
                    rel="noreferrer"
                    className="rounded-sm text-fg-3 underline underline-offset-[3px] hover:text-fg focus-ring"
                  >
                    Runbooks
                  </a>
                ) : null,
              ]
        }
        secondary={
          <Button
            variant="outline"
            className="max-lg:h-(--vs-control-touch)"
            onClick={() => alerts.refetch()}
            pending={refreshing}
          >
            {refreshing ? null : <RotateCcw aria-hidden />}
            {alerts.isFetching ? "Checking" : "Check now"}
          </Button>
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

// the runbook of every alert, opened where it says which channels the
// alerts reach
const RUNBOOKS = runbookUrl("docs/runbooks/alerts.md#where-alerts-show");

// where the alerts come from: the alert service and its servers, the
// addresses on hover
function SourceFact({ error, data }: { error: unknown; data: OperatorAlerts | null }) {
  if (error || !data)
    return (
      <span className="inline-flex items-center gap-1" data-sources="">
        <StatusDot tone="critical" />
        {error instanceof ApiError && error.code === "alert_source_not_configured"
          ? "None set up"
          : "Not answering"}
      </span>
    );
  const answered = data.sources.filter((s) => s.reachable).length;
  return (
    <span
      className="inline-flex items-center gap-1"
      title={data.sources.map((s) => s.address).join(", ")}
      data-sources=""
    >
      <StatusDot tone={answered === data.sources.length ? "success" : "warning"} />
      Alertmanager,{" "}
      {answered === data.sources.length
        ? countOf(data.sources.length, "server")
        : `${answered} of ${countOf(data.sources.length, "server")}`}
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
      The always-on test alert is missing. An alert can fire without showing here.
    </Notice>
  );
}

function SourcesDown({ down, total }: { down: AlertSource[]; total: number }) {
  return (
    <Notice
      tone="warning"
      title={`${down.length} of ${countOf(total, "alert server")} did not answer`}
    >
      <ul className="flex flex-col gap-1 font-mono text-caption text-fg-2">
        {down.map((s) => (
          <li key={s.address} className="break-all">
            {s.address}: {s.error}
          </li>
        ))}
      </ul>
    </Notice>
  );
}

function SeverityList({ group, now }: { group: SeverityGroup; now: number }) {
  const headingId = `severity-${group.key || "none"}`;
  return (
    <section aria-labelledby={headingId} className="flex flex-col gap-2.5" data-severity={group.key}>
      <h2 id={headingId} className="flex items-baseline gap-2 text-[15px] font-semibold text-fg">
        {groupTitle(group.key)}
        <span className="text-small font-normal text-fg-3 tabular">{group.alerts.length}</span>
      </h2>
      <ul className="flex flex-col overflow-hidden rounded-2xl border border-line bg-raised">
        {group.alerts.map((a) => (
          <li key={a.fingerprint} className="border-t border-line-subtle first:border-t-0">
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
  muted: "text-fg-3",
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

// one alert as one row: the severity icon, the alert as one sentence with
// its labels as quiet tags, how long it has fired, and the runbook. the
// description and the held state are on the sentence's hover, so the row
// carries no second line of prose
function AlertItem({ alert, now }: { alert: FiringAlert; now: number }) {
  const runbook = runbookUrl(alert.runbook);
  const urgency = alertUrgency(alert);
  const Icon = TONE_ICON[urgency.tone];
  const held = alert.state === "suppressed";
  const heldText = held
    ? alert.inhibited_by.length > 0
      ? HELD_TEXT.inhibited
      : HELD_TEXT.suppressed
    : alert.state === "unprocessed"
      ? HELD_TEXT.unprocessed
      : null;
  const labels = [
    alert.name,
    alert.node ? `node ${alert.node}` : null,
    alert.service ? `service ${alert.service}` : null,
    alert.instance ? `instance ${alert.instance}` : null,
  ].filter((x): x is string => x !== null);
  const sentence = sentenceCase(alert.summary ?? alert.name);
  const description = alert.description ? sentenceCase(alert.description) : null;
  return (
    <article
      aria-label={sentence}
      data-alert={alert.name}
      className={cn(
        "grid min-w-0 grid-cols-[20px_minmax(0,1fr)] items-center gap-x-4 gap-y-2 px-5 py-3.5 md:grid-cols-[20px_minmax(0,1fr)_14rem_7rem]",
        held && "opacity-70",
      )}
    >
      <span className={cn("flex items-center self-start pt-0.5 md:self-center", TONE_TEXT[urgency.tone])}>
        <Icon aria-hidden className="size-4" />
        <span className="sr-only">{urgency.word}</span>
      </span>
      <div className="flex min-w-0 flex-col">
        <p className="text-body font-medium break-words text-fg" title={description ?? undefined}>
          {sentence}
        </p>
        {description ? <span className="sr-only">{description}</span> : null}
        <ul className="mt-1.5 flex flex-wrap gap-1.5" data-labels="">
          {labels.map((l) => (
            <li
              key={l}
              className="rounded-[5px] border border-line-2 px-[7px] py-px font-mono text-[11.5px] break-all text-fg-3"
            >
              {l}
            </li>
          ))}
          {heldText ? (
            <li
              title={heldText}
              className="rounded-[5px] border border-line-2 px-[7px] py-px text-[11.5px] text-fg-3"
            >
              {held ? "Held back" : "Just received"}
              <span className="sr-only">. {heldText}</span>
            </li>
          ) : null}
        </ul>
      </div>
      <p
        className="col-start-2 flex items-baseline gap-2 text-[13.5px] tabular md:col-start-3"
        title={formatDateTimeUtc(alert.starts_at)}
      >
        <span className="sr-only">Fires for</span>
        <b className="font-semibold text-fg">{firingFor(alert.starts_at, now)}</b>
        <span className="text-fg-3">
          since <time dateTime={alert.starts_at}>{formatTimeUtc(alert.starts_at).replace(" UTC", "")}</time>
        </span>
      </p>
      {runbook ? (
        <a
          href={runbook}
          target="_blank"
          rel="noreferrer"
          className="col-start-2 inline-flex items-center gap-1 justify-self-start rounded-sm text-small text-fg-2 hover:text-fg focus-ring max-md:min-h-(--vs-control-touch) md:col-start-4 md:justify-self-end"
        >
          Runbook<span className="sr-only"> for {alert.name}</span>
          <ArrowUpRight aria-hidden className="size-3" />
        </a>
      ) : null}
    </article>
  );
}
