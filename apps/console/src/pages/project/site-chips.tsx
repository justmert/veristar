// the site filter of the overview and web pages: one chip per site with its
// health dot, all sites first, add site last; and the count of visitors
// right now on the right of the same row. a project has one to three
// sites, so a chip row reads faster than a select. `required` is the same
// row as a single choice with no "all" chip and no dots (privacy).

import { useQueries, useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { useState } from "react";
import { ingestMonthQuery } from "@/api/queries";
import type { Site } from "@/api/schemas";
import { webRealtimeQuery } from "@/api/web";
import { AddChip, ChipRadioGroup, FilterChip } from "@/components/filter-chip";
import { StatusDot } from "@/components/status";
import { Skeleton as Bone } from "@/components/ui/skeleton";
import { Tooltip } from "@/components/ui/tooltip";
import { formatCount, formatTimeUtc } from "@/lib/format";
import { type FirstEvent, firstEvent } from "@/lib/site-setup";
import { AddSiteDialog } from "@/pages/project/sites";
import { waitsOnProvisioning } from "@/query-client";

type Health = { tone: "success" | "warning" | "critical" | "neutral"; note: string | null };

// what the dot of a site says: events arrive, none yet, only refused ones,
// or the site waits for an operator
function healthOf(site: Site, event: FirstEvent | null): Health {
  if (site.in_review || site.status === "pending") return { tone: "neutral", note: "in review" };
  if (site.status !== "active") return { tone: "neutral", note: site.status };
  if (event === null) return { tone: "neutral", note: null };
  if (event === "received") return { tone: "success", note: null };
  if (event === "refused") return { tone: "critical", note: "events refused" };
  return { tone: "warning", note: "no event yet" };
}

// the first event of every active site from the edge counts of 30 days
export function useSiteHealth(sites: Site[]): Map<string, Health> {
  const active = sites.filter((s) => s.status === "active" && !s.in_review);
  const answers = useQueries({ queries: active.map((s) => ingestMonthQuery(s.id)) });
  const out = new Map<string, Health>();
  for (const s of sites) {
    const i = active.indexOf(s);
    const d = i >= 0 ? answers[i]?.data : undefined;
    out.set(s.id, healthOf(s, d ? firstEvent(d) : null));
  }
  return out;
}

export function SiteChips(
  props:
    | {
        required?: false;
        sites: Site[];
        value: string | null;
        onChange: (site: string | null) => void;
        tenantId: string;
        canAdd?: boolean;
        allLabel?: string;
      }
    | {
        // a required single choice: no "all" chip, no health dots
        required: true;
        sites: Site[];
        value: string | null;
        onChange: (site: string) => void;
        // the id of the visible label of the choice
        labelledBy?: string;
      },
) {
  const { sites } = props;
  // a required choice shows no health, so it asks for none
  const health = useSiteHealth(props.required ? [] : sites);
  const [adding, setAdding] = useState(false);
  // two sites of one name are told apart by their domain
  const names = new Map<string, number>();
  for (const x of sites) names.set(x.name, (names.get(x.name) ?? 0) + 1);
  const nameOf = (s: Site) => ((names.get(s.name) ?? 0) > 1 ? s.domain : s.name);
  if (props.required)
    return (
      <ChipRadioGroup
        label={props.labelledBy ? undefined : "Site"}
        labelledBy={props.labelledBy}
        value={props.value}
        options={sites.map((s) => ({ value: s.id, label: nameOf(s), title: s.domain }))}
        onChange={props.onChange}
      />
    );
  const { value, onChange, tenantId, canAdd = false, allLabel = "All sites" } = props;
  const many = sites.length > 1;
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-1.5">
      <fieldset className="contents">
        <legend className="sr-only">Site</legend>
        {many ? (
          <FilterChip on={value === null} onClick={() => onChange(null)}>
            {allLabel}
          </FilterChip>
        ) : null}
        {sites.map((s) => {
          const h = health.get(s.id) ?? { tone: "neutral" as const, note: null };
          return (
            <FilterChip
              key={s.id}
              data-site-chip={s.id}
              on={value === s.id || (!many && value === null)}
              onClick={() => onChange(many ? s.id : null)}
              title={s.domain}
            >
              <StatusDot tone={h.tone} />
              <span className="truncate">{nameOf(s)}</span>
              {h.note ? <span className="text-caption text-fg-3">{h.note}</span> : null}
            </FilterChip>
          );
        })}
      </fieldset>
      {canAdd ? (
        <>
          <AddChip onClick={() => setAdding(true)}>Add site</AddChip>
          <AddSiteDialog tenantId={tenantId} open={adding} onOpenChange={setAdding} />
        </>
      ) : null}
    </div>
  );
}

// visitors in the last 5 minutes, asked every 30 seconds. the web page adds
// the page views per minute of the last 30 minutes as a small bar chart
export function LiveCount({
  tenantId,
  site,
  minutes: withMinutes = false,
}: {
  tenantId: string;
  site: string | null;
  minutes?: boolean;
}) {
  const rt = useQuery(webRealtimeQuery(tenantId, site));
  if (waitsOnProvisioning(rt))
    return (
      <span className="text-caption text-fg-3" data-testid="web-active-pending">
        Being set up
      </span>
    );
  if (rt.isPending) return <Bone className="h-6 w-32" />;
  if (rt.error)
    return (
      <span className="text-caption text-fg-3" data-testid="web-active-error">
        Visitors right now did not load
      </span>
    );
  const d = rt.data;
  const minutes = d.minutes;
  const max = Math.max(1, ...minutes.map((m) => m.pageviews));
  const when = `Visitors in the last 5 minutes, checked ${formatTimeUtc(d.at, { seconds: true })} UTC, every 30 s`;
  const body = (
    <span
      className={cn(
        "inline-flex items-center gap-2 text-small text-fg-2",
        withMinutes && "h-8 rounded-full border border-line bg-raised px-3",
      )}
    >
      <StatusDot tone={d.visitors > 0 ? "success" : "neutral"} />
      <span className="font-semibold text-fg tabular" data-testid="web-active">
        {formatCount(d.visitors)}
      </span>
      <span>{withMinutes ? "now" : "visitors now"}</span>
      {withMinutes && minutes.length ? (
        <span
          role="img"
          aria-label={`Page views per minute, last 30 minutes: ${minutes.map((m) => m.pageviews).join(", ")}`}
          className="flex h-4 w-24 items-end gap-px"
        >
          {minutes.map((m) => (
            <span
              key={m.at}
              className={cn("flex-1 rounded-t-[1px]", m.pageviews > 0 ? "bg-success" : "bg-line")}
              // the height is data; react writes it through the cssom
              style={{ height: `${Math.max(12, (m.pageviews / max) * 100)}%` }}
            />
          ))}
        </span>
      ) : null}
    </span>
  );
  return (
    <Tooltip content={when}>
      {/* biome-ignore lint/a11y/noNoninteractiveTabindex: when it was checked opens on focus too */}
      <span tabIndex={0} className="rounded-full focus-ring">
        {body}
        <span className="sr-only">. {when}</span>
      </span>
    </Tooltip>
  );
}
