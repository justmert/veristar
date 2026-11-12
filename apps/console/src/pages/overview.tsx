import { useQuery } from "@tanstack/react-query";
import { Code2, ExternalLink, Plus } from "lucide-react";
import { Link } from "react-router";
import { webDashboardUrl } from "@/api/endpoints";
import { ingestDayQuery, ingestMonthQuery, sitesQuery } from "@/api/queries";
import type { Ingest, Site } from "@/api/schemas";
import { DbIpAttribution } from "@/components/dbip-attribution";
import { ErrorPanel, Loading } from "@/components/error-panel";
import { StatusPill } from "@/components/status";
import { Button } from "@/components/ui/button";
import { formatCount, formatHourUtc } from "@/lib/format";
import { outcomeHint, rejectionRows, sumRejected, sumUnclassified } from "@/lib/outcomes";
import { useProject } from "./project-layout";

export function OverviewPage() {
  const { tenant } = useProject();
  const sites = useQuery(sitesQuery(tenant.id));

  if (sites.isPending) return <Loading label="Loading sites" />;
  if (sites.error) return <ErrorPanel error={sites.error} onRetry={() => sites.refetch()} />;
  const live = sites.data.filter((s) => s.status !== "removed");

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <h2 className="text-xl font-semibold tracking-tight">Traffic by site</h2>
        <p className="text-sm text-muted-foreground">Counts in UTC, updated every few minutes.</p>
      </div>

      {live.length === 0 ? (
        <div className="rounded-xl border border-dashed bg-card p-8">
          <h3 className="font-semibold">No sites yet</h3>
          <p className="mt-1 max-w-prose text-muted-foreground">
            Add the website you want to measure. You get a script tag to paste into its pages.
          </p>
          <Button asChild className="mt-4">
            <Link to={`/projects/${tenant.id}/sites`}>
              <Plus aria-hidden />
              Add a site
            </Link>
          </Button>
        </div>
      ) : (
        <ul className="space-y-4">
          {live.map((s) => (
            <li key={s.id}>
              <SiteTraffic site={s} />
            </li>
          ))}
        </ul>
      )}

      <DbIpAttribution />
    </div>
  );
}

function SiteTraffic({ site }: { site: Site }) {
  const day = useQuery(ingestDayQuery(site.id));
  const month = useQuery(ingestMonthQuery(site.id));

  return (
    <article className="rounded-xl border bg-card" aria-labelledby={`site-${site.id}`}>
      <header className="flex flex-wrap items-start gap-x-4 gap-y-3 border-b p-4 sm:p-5">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <h3 id={`site-${site.id}`} className="truncate text-lg font-semibold">
              {site.name}
            </h3>
            <StatusPill status={site.status} />
          </div>
          <p className="truncate text-sm text-muted-foreground">{site.domain}</p>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button asChild>
            <a href={webDashboardUrl(site.id, "primary")}>
              <ExternalLink aria-hidden />
              Open web dashboard
            </a>
          </Button>
          {site.quarantine_website_id ? (
            <Button asChild variant="outline">
              <a href={webDashboardUrl(site.id, "quarantine")}>Quarantine view</a>
            </Button>
          ) : null}
          <Button asChild variant="ghost">
            <Link to={`/projects/${site.tenant_id}/sites/${site.id}/snippet`}>
              <Code2 aria-hidden />
              Snippet
            </Link>
          </Button>
        </div>
      </header>

      <div className="grid gap-6 p-4 sm:p-5 lg:grid-cols-[1fr_1fr]">
        <section aria-label="Last 24 hours">
          <h4 className="text-sm font-medium">Last 24 hours</h4>
          {day.isPending ? <Loading label="Loading counts" /> : null}
          {day.error ? <ErrorPanel error={day.error} onRetry={() => day.refetch()} /> : null}
          {day.data ? (
            <>
              <Totals ingest={day.data} />
              <HourStrip ingest={day.data} />
            </>
          ) : null}
        </section>
        <section aria-label="Last 30 days">
          <h4 className="text-sm font-medium">Last 30 days</h4>
          {month.isPending ? <Loading label="Loading counts" /> : null}
          {month.error ? <ErrorPanel error={month.error} onRetry={() => month.refetch()} /> : null}
          {month.data ? (
            <>
              <Totals ingest={month.data} />
              <Rejections totals={month.data.totals} />
            </>
          ) : null}
        </section>
      </div>
    </article>
  );
}

const n = (totals: Record<string, number>, k: string) => totals[k] ?? 0;

export function Totals({ ingest }: { ingest: Ingest }) {
  const t = ingest.totals;
  const unclassified = sumUnclassified(t);
  const rows = [
    { key: "accepted", label: "Accepted", value: n(t, "accepted"), tone: "text-good" },
    { key: "quarantined", label: "Quarantined", value: n(t, "quarantined"), tone: "text-warn" },
    { key: "bot", label: "Bots", value: n(t, "bot"), tone: "" },
    { key: "rejected", label: "Rejected", value: sumRejected(t), tone: "" },
    { key: "dlq", label: "Not delivered", value: n(t, "dlq"), tone: "" },
    ...(unclassified > 0 ? [{ key: "other", label: "Other", value: unclassified, tone: "" }] : []),
  ];
  return (
    <dl className="mt-3 grid grid-cols-3 gap-x-4 gap-y-3 sm:grid-cols-5">
      {rows.map((r) => (
        <div key={r.key} title={outcomeHint(r.key) ?? undefined}>
          <dt className="text-xs text-muted-foreground">{r.label}</dt>
          <dd className={`tabular text-xl font-semibold tracking-tight ${r.tone}`}>
            {formatCount(r.value)}
          </dd>
        </div>
      ))}
    </dl>
  );
}

export type HourSlot = { at: string; accepted: number; quarantined: number };

// one slot per utc hour from `from` up to `to`; the route omits empty hours,
// which are zero, not unknown
export function hourSlots(ingest: Ingest): HourSlot[] {
  const byAt = new Map(ingest.series.map((p) => [Date.parse(p.at), p.counts]));
  const out: HourSlot[] = [];
  const end = Date.parse(ingest.to);
  for (let t = Date.parse(ingest.from); t < end; t += 3600_000) {
    const c = byAt.get(t) ?? {};
    out.push({
      at: new Date(t).toISOString().replace(/\.\d{3}Z$/, "Z"),
      accepted: c.accepted ?? 0,
      quarantined: c.quarantined ?? 0,
    });
  }
  return out;
}

function HourStrip({ ingest }: { ingest: Ingest }) {
  const slots = hourSlots(ingest);
  const max = Math.max(1, ...slots.map((s) => s.accepted + s.quarantined));
  const first = slots[0];
  const last = slots[slots.length - 1];
  const H = 56;
  const w = 100 / Math.max(slots.length, 1);
  const peak = slots.reduce<HourSlot | null>(
    (a, b) => (a === null || b.accepted > a.accepted ? b : a),
    null,
  );
  const summary = peak
    ? `Accepted events per hour. Busiest hour ${formatHourUtc(peak.at)} with ${formatCount(peak.accepted)}.`
    : "No hours in range.";

  return (
    <figure className="mt-4">
      <svg
        role="img"
        aria-label={summary}
        viewBox={`0 0 100 ${H}`}
        preserveAspectRatio="none"
        className="h-14 w-full"
      >
        <line
          x1="0"
          x2="100"
          y1={H - 0.5}
          y2={H - 0.5}
          className="stroke-border"
          strokeWidth="1"
          vectorEffect="non-scaling-stroke"
        />
        {slots.map((s, i) => {
          const a = (s.accepted / max) * (H - 2);
          const q = (s.quarantined / max) * (H - 2);
          const x = i * w + w * 0.15;
          const bw = w * 0.7;
          return (
            <g key={s.at}>
              <title>{`${formatHourUtc(s.at)}: ${formatCount(s.accepted)} accepted, ${formatCount(s.quarantined)} quarantined`}</title>
              <rect x={x} width={bw} y={H - 1 - a} height={a} className="fill-good" />
              <rect x={x} width={bw} y={H - 1 - a - q} height={q} className="fill-warn" />
            </g>
          );
        })}
      </svg>
      <figcaption className="mt-1 flex justify-between text-xs text-muted-foreground">
        <span>{first ? formatHourUtc(first.at) : ""}</span>
        <span className="flex items-center gap-3">
          <span className="inline-flex items-center gap-1">
            <span aria-hidden className="size-2 rounded-sm bg-good" />
            Accepted
          </span>
          <span className="inline-flex items-center gap-1">
            <span aria-hidden className="size-2 rounded-sm bg-warn" />
            Quarantined
          </span>
        </span>
        <span>
          {last ? formatHourUtc(new Date(Date.parse(last.at) + 3600_000).toISOString()) : ""}
        </span>
      </figcaption>
    </figure>
  );
}

function Rejections({ totals }: { totals: Record<string, number> }) {
  const rows = rejectionRows(totals);
  if (rows.length === 0)
    return <p className="mt-4 text-sm text-muted-foreground">No rejected events.</p>;
  return (
    <details className="mt-4 text-sm">
      <summary className="cursor-pointer text-muted-foreground hover:text-foreground">
        Why events were rejected
      </summary>
      <table className="mt-2 w-full">
        <tbody>
          {rows.map((r) => (
            <tr key={r.key} className="border-b last:border-0">
              <td className="py-1.5 pr-4" title={outcomeHint(r.key) ?? undefined}>
                {r.label}
              </td>
              <td className="tabular py-1.5 text-right">{formatCount(r.count)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </details>
  );
}
