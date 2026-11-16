import { useQuery } from "@tanstack/react-query";
import { Download } from "lucide-react";
import type { ReactNode } from "react";
import { exportPaths, type SCFView, scfViewQuery } from "@/api/onchain";
import { ErrorPanel, Loading, StateBlock } from "@/components/error-panel";
import { projectTabs } from "@/components/nav/nav-items";
import { NavTabs } from "@/components/nav/nav-tabs";
import { DayStrip } from "@/components/onchain";
import { Fact, PageHeader, Panel } from "@/components/panel";
import { Pill } from "@/components/status";
import { Button } from "@/components/ui/button";
import { formatCount } from "@/lib/format";
import { formatDuration, formatShare } from "@/lib/global";
import { useProject } from "./project-layout";

const RANGE = {};

// "what scf sees": the figures the operator views show about this project,
// read through the operator views' own queries for the same range, so the
// numbers and ranges are the ones reviewers see. no score is added here.
export function SCFViewPage() {
  const { tenant } = useProject();
  const q = useQuery(scfViewQuery(tenant.id, RANGE));
  if (q.isPending) return <Loading label="Loading what SCF sees" />;
  if (q.error) return <ErrorPanel error={q.error} onRetry={() => q.refetch()} />;
  const v = q.data;
  return (
    <div className="flex flex-col gap-5">
      <NavTabs label="On-chain" items={projectTabs(tenant.id, "onchain") ?? []} />
      <PageHeader
        title="What SCF sees"
        description={`The figures the SCF operator views show about this project for ${v.from} to ${v.to} (UTC days), from the same queries. Nothing here is hidden from you, and nothing is scored beyond what is shown.`}
        actions={
          <Button asChild variant="outline">
            <a href={exportPaths.overview(tenant.id, { from: v.from, to: v.to })} download>
              <Download aria-hidden />
              Export CSV
            </a>
          </Button>
        }
      />
      {!v.in_operator_views ? (
        <StateBlock kind="empty" title="Not listed in the operator views">
          This project is internal to the platform, so the SCF views do not list it.
        </StateBlock>
      ) : (
        <SCFRows v={v} />
      )}
    </div>
  );
}

// the integrity grade slot: it renders only when the control plane sends
// the field, and shows exactly what it sends
export function IntegritySlot({ value }: { value: SCFView["integrity"] }) {
  if (!value) return null;
  return (
    <Panel
      title="Integrity grade"
      description="The grade of your project's row in the operators' Global web view for this range."
    >
      {value.grade === null ? (
        <p className="text-body text-fg-2">No site day of this range has a grade yet.</p>
      ) : (
        <div className="flex flex-col gap-1">
          <span className="text-page text-fg" data-testid="integrity-grade">
            {value.grade}
          </span>
          <span className="text-small text-fg-2">
            {value.graded_days} graded site days: {value.days_a} A, {value.days_b} B, {value.days_c}{" "}
            C
          </span>
        </div>
      )}
    </Panel>
  );
}

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <Fact label={label}>
      <span className="tabular-nums">{children}</span>
    </Fact>
  );
}

function SCFRows({ v }: { v: SCFView }) {
  const web = v.web;
  const api = v.api;
  const chain = v.onchain;
  return (
    <>
      <IntegritySlot value={v.integrity} />
      <Panel title="Project" description="As the operator list shows it.">
        <dl className="grid grid-cols-2 gap-4 sm:grid-cols-4">
          <Row label="Status">{v.tenant?.status ?? "n/a"}</Row>
          <Row label="Sites">{v.sites ? formatCount(v.sites.sites) : "n/a"}</Row>
          <Row label="Verified sites">{v.sites ? formatCount(v.sites.sites_verified) : "n/a"}</Row>
          <Row label="Sites with stellar.toml">
            {v.sites ? formatCount(v.sites.sites_with_stellar_toml) : "n/a"}
          </Row>
        </dl>
      </Panel>
      {web ? (
        <Panel title="Web" description="The project's row of the Global web view.">
          <dl className="grid grid-cols-2 gap-4 sm:grid-cols-4">
            <Row label="Page views">{formatCount(web.pageviews)}</Row>
            <Row label="Visitors">{formatCount(web.visitors)}</Row>
            <Row label="Visits">{formatCount(web.visits)}</Row>
            <Row label="Bounce rate">
              {web.bounce_rate === null ? "n/a" : formatShare(web.bounce_rate)}
            </Row>
            <Row label="Average visit">
              {web.avg_visit_duration === null ? "n/a" : formatDuration(web.avg_visit_duration)}
            </Row>
            <Row label="Quarantined share">
              {web.quarantine_share === null ? "n/a" : formatShare(web.quarantine_share)}
            </Row>
            <Row label="Gateway requests">{formatCount(web.ingest_requests)}</Row>
            <Row label="Rejected share">
              {web.rejected_share === null ? "n/a" : formatShare(web.rejected_share)}
            </Row>
          </dl>
          <div className="mt-4">
            <DayStrip
              label="Daily page views"
              points={v.web_series.map((p) => ({
                day: p.day,
                value: p.aggregated ? p.pageviews : null,
                missing: p.aggregated ? null : "not_read_yet",
              }))}
            />
          </div>
        </Panel>
      ) : null}
      {api ? (
        <Panel title="API" description="The project's row of the Global API view.">
          <dl className="grid grid-cols-2 gap-4 sm:grid-cols-4">
            <Row label="Requests">{formatCount(api.requests)}</Row>
            <Row label="4xx rate">{api.rate_4xx === null ? "n/a" : formatShare(api.rate_4xx)}</Row>
            <Row label="5xx rate">{api.rate_5xx === null ? "n/a" : formatShare(api.rate_5xx)}</Row>
            <Row label="p95">
              {api.p95_over_10s
                ? "over 10 s"
                : api.p95_seconds === null
                  ? "n/a"
                  : `${api.p95_seconds.toFixed(3)} s`}
            </Row>
            <Row label="Services">{formatCount(api.services)}</Row>
            <Row label="Routes">{formatCount(api.routes)}</Row>
          </dl>
        </Panel>
      ) : null}
      {chain ? (
        <Panel
          title="On-chain"
          description="The project's on-chain column."
          actions={
            v.link_two_way ? <Pill tone="success">stellar.toml linked both ways</Pill> : null
          }
        >
          <dl className="grid grid-cols-2 gap-4 sm:grid-cols-4">
            <Row label="Operations">{formatCount(chain.operations)}</Row>
            <Row label="Transactions">{formatCount(chain.transactions)}</Row>
            <Row label="Verified ids">{`${chain.ids_verified} of ${chain.ids}`}</Row>
            <Row label="Days without data">{formatCount(chain.days_missing)}</Row>
          </dl>
          <div className="mt-4">
            <DayStrip
              label="Daily operations"
              points={v.onchain_series.map((p) => ({
                day: p.day,
                value: p.operations,
                missing: null,
              }))}
            />
          </div>
        </Panel>
      ) : null}
    </>
  );
}
