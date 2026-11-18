import { useState } from "react";
import type { Ingest, Site } from "@/api/schemas";
import { DbIpAttribution } from "@/components/dbip-attribution";
import { Panel } from "@/components/panel";
import { Segmented } from "@/components/segmented";
import { Table, TableBody, TableCell, TableRow } from "@/components/ui/table";
import { useWebTraffic, WEB_RANGES, type WebRange, WebTraffic } from "@/components/web-charts";
import { formatCount } from "@/lib/format";
import { outcomeHint, rejectionRows, sumRejected, sumUnclassified } from "@/lib/outcomes";

// what happened to every event of one site: accepted, quarantined, bots,
// rejected with the reasons, not delivered. shown on Web when a site is
// chosen, so a total never silently loses events.
export function SiteOutcomes({ site }: { site: Site }) {
  const [range, setRange] = useState<WebRange>("30d");
  return (
    <section id="outcomes" aria-labelledby="outcomes-h" className="flex scroll-mt-6 flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 id="outcomes-h" className="text-section text-fg">
          Every event of {site.name}
        </h2>
        <Segmented
          label="Outcome range"
          value={range}
          options={WEB_RANGES}
          onChange={setRange}
          hint={range === "24h" ? "per hour" : "per day"}
        />
      </div>
      <SiteTraffic site={site} range={range} />
    </section>
  );
}

function SiteTraffic({ site, range }: { site: Site; range: WebRange }) {
  const data = useWebTraffic([site], range);
  const answer = data.bySite[0];
  return (
    <div className="flex flex-col gap-4">
      <WebTraffic
        data={data}
        range={range}
        label={range === "24h" ? "Last 24 hours" : "Last 30 days"}
      />
      {answer ? <Outcomes ingest={answer} range={range} /> : null}
      <div className="flex flex-col gap-1">
        <p className="text-caption font-normal text-fg-3">
          Pages, referrers and countries are in the web dashboard. Counts here are per UTC{" "}
          {range === "24h" ? "hour" : "day"} and update every few minutes.
        </p>
        <DbIpAttribution />
      </div>
    </div>
  );
}

const n = (totals: Record<string, number>, k: string) => totals[k] ?? 0;

// every outcome of the range by name, so a total never silently loses events,
// and why events were rejected
function Outcomes({ ingest, range }: { ingest: Ingest; range: WebRange }) {
  const t = ingest.totals;
  const unclassified = sumUnclassified(t);
  const rows = [
    { key: "accepted", label: "Accepted", value: n(t, "accepted") },
    { key: "quarantined", label: "Quarantined", value: n(t, "quarantined") },
    { key: "bot", label: "Bots", value: n(t, "bot") },
    { key: "rejected", label: "Rejected", value: sumRejected(t) },
    { key: "dlq", label: "Not delivered", value: n(t, "dlq") },
    ...(unclassified > 0 ? [{ key: "other", label: "Other", value: unclassified }] : []),
  ];
  const reasons = rejectionRows(t);
  return (
    <Panel
      title="Every outcome"
      description={`What happened to the events of the ${range === "24h" ? "last 24 hours" : "last 30 days"}`}
    >
      <dl className="grid grid-cols-2 gap-x-6 gap-y-3 sm:grid-cols-3 lg:grid-cols-6">
        {rows.map((r) => (
          <div key={r.key} className="flex flex-col gap-1" title={outcomeHint(r.key) ?? undefined}>
            <dt className="text-caption text-fg-3">{r.label}</dt>
            <dd className="font-mono text-title text-fg tabular">{formatCount(r.value)}</dd>
          </div>
        ))}
      </dl>
      {reasons.length === 0 ? (
        <p className="mt-4 text-small text-fg-2">No rejected events.</p>
      ) : (
        <details className="mt-4 text-small">
          <summary className="w-fit cursor-pointer rounded-sm text-fg-2 transition-colors duration-(--vs-duration-press) hover:text-fg focus-ring">
            Why events were rejected
          </summary>
          <Table className="mt-2">
            <TableBody>
              {reasons.map((r) => (
                <TableRow key={r.key}>
                  <TableCell>
                    <span className="flex flex-col">
                      <span className="text-fg">{r.label}</span>
                      {outcomeHint(r.key) ? (
                        <span className="text-caption font-normal text-fg-3">
                          {outcomeHint(r.key)}
                        </span>
                      ) : null}
                    </span>
                  </TableCell>
                  <TableCell className="text-right font-mono">{formatCount(r.count)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </details>
      )}
    </Panel>
  );
}
