import { useQuery } from "@tanstack/react-query";
import { fade } from "@veristar/brand/motion";
import { CircleAlert, ExternalLink, Settings, Trash2 } from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
import { type ReactNode, useEffect, useId, useState } from "react";
import { Link, useLocation, useNavigate, useParams, useSearchParams } from "react-router";
import { api } from "@/api/endpoints";
import { keys, sitesQuery, snippetQuery } from "@/api/queries";
import type { Ingest, Site, Snippet } from "@/api/schemas";
import { CodeBlock, CopyField } from "@/components/code-block";
import { Disclosure } from "@/components/disclosure";
import { StellarTomlSummary } from "@/components/evidence";
import { NativeSelect } from "@/components/form-field";
import { Notice } from "@/components/notice";
import { PageHeader } from "@/components/page-header";
import { RowMenu } from "@/components/row-menu";
import { Segmented } from "@/components/segmented";
import { type SetupStep as BarStep, SetupProgress } from "@/components/setup-progress";
import { ErrorState } from "@/components/states";
import { StatusDot } from "@/components/status";
import { StatusBadge } from "@/components/status-badge";
import { toast } from "@/components/toast";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Skeleton } from "@/components/ui/skeleton";
import { isNotFound } from "@/lib/errors";
import { formatBytes, formatCount, formatDate } from "@/lib/format";
import { siblingUrl } from "@/lib/hosts";
import { outcomeHint, rejectionRows } from "@/lib/outcomes";
import { CONSENT_START, readsTitleChoice, snippetGuides } from "@/lib/snippet-guides";
import { webPath } from "@/lib/web-dashboard";
import { useProject } from "@/pages/project-layout";
import { NotFoundPage } from "@/pages/public/not-found";
import { queryClient } from "@/query-client";
import { SitePageLoading } from "./page-frame";
import { SiteSettings } from "./site-settings";
import { VerifyDomain } from "./site-verify";
import { siteSteps, useFirstEvent } from "./sites";

// one site on one page: its setup (the current step large, later steps on
// one line, done steps as quiet rows) and its settings under it. the old
// settings address redirects here (site-settings.tsx).
export function SiteSetupPage() {
  const { tenant } = useProject();
  const { siteId = "" } = useParams();
  const sites = useQuery(sitesQuery(tenant.id));
  const site = sites.data?.find((s) => s.id === siteId && s.status !== "removed");

  if (sites.data && !site) return <NotFoundPage />;
  if (sites.error)
    return (
      <div className="flex flex-col gap-6">
        <PageHeader title="Site" crumb={<SitesCrumb tenantId={tenant.id} />} />
        <ErrorState
          title="The site did not load"
          error={sites.error}
          onRetry={() => sites.refetch()}
        />
      </div>
    );
  if (!site) return <SitePageLoading tenantId={tenant.id} page="setup" />;
  return <SitePage site={site} />;
}

function SitesCrumb({ tenantId }: { tenantId: string }) {
  return (
    <>
      <Link
        to={`/projects/${tenantId}/sites`}
        className="rounded-sm transition-colors duration-(--vs-duration-press) hover:text-fg focus-ring"
      >
        Sites
      </Link>
      <span aria-hidden>/</span>
    </>
  );
}

// which done step shows its body under the setup
type Shown = "tag" | "record" | "toml" | null;

function SitePage({ site }: { site: Site }) {
  const { isOwner, termsCurrent } = useProject();
  const [params, setParams] = useSearchParams();
  const version = params.get("version");
  const snippet = useQuery(snippetQuery(site.id, version));
  const first = useFirstEvent(site);
  const [fw, setFw] = useState("html");
  const [shown, setShown] = useState<Shown>(null);
  const docs = siblingUrl(window.location.hostname, "docs");
  const navigate = useNavigate();
  // a link to one part (#domain, #settings) scrolls to it once the page is
  // drawn, after the shell's own scroll to the top of a new page
  const { hash } = useLocation();
  useEffect(() => {
    if (!hash) return;
    const frame = requestAnimationFrame(() =>
      document.getElementById(hash.slice(1))?.scrollIntoView?.({ block: "start" }),
    );
    return () => cancelAnimationFrame(frame);
  }, [hash]);

  if (isNotFound(snippet.error)) return <NotFoundPage />;

  const event = first.event;
  const counted = first.month ? (first.month.totals.accepted ?? 0) : null;
  const bar = siteSteps(site, event);
  const toggle = (s: Exclude<Shown, null>) => setShown((x) => (x === s ? null : s));
  const toml = site.stellar_toml;
  const tomlFound = toml?.status === "found";
  const steps: BarStep[] = bar.map((s) => ({
    ...s,
    result:
      s.key === "install" ? (
        <>
          <span>Installed</span>
          <button type="button" aria-expanded={shown === "tag"} onClick={() => toggle("tag")}>
            {shown === "tag" ? "Hide the tag" : "Show the tag"}
          </button>
        </>
      ) : s.key === "event" ? (
        <span>Received</span>
      ) : (
        <>
          {site.verify_method ? (
            <span>{site.verify_method === "dns" ? "DNS record" : "File on the site"}</span>
          ) : null}
          {site.verified_at ? <span>{formatDate(site.verified_at)}</span> : null}
          <button
            type="button"
            aria-expanded={shown === "record"}
            onClick={() => toggle("record")}
          >
            {shown === "record" ? "Hide the record" : "Show the record"}
          </button>
        </>
      ),
  }));
  // a done step's body only shows while that step is still done
  const open: Shown =
    shown === "tag" && bar[0]?.state === "done"
      ? "tag"
      : shown === "record" && bar[2]?.state === "done"
        ? "record"
        : shown === "toml"
          ? "toml"
          : null;
  const current = bar.find((s) => s.state === "current" || s.state === "problem");
  const guides = snippet.data ? snippetGuides(snippet.data, { consent: false, title: false }) : [];
  const canDelete = isOwner && termsCurrent;

  const installBody = snippet.isPending ? (
    <div role="status" aria-busy="true" className="flex flex-col gap-3">
      <span className="sr-only">Loading the tag</span>
      <Skeleton className="h-16 w-full" />
    </div>
  ) : snippet.error ? (
    <ErrorState
      title="The tag did not load"
      error={snippet.error}
      onRetry={() => snippet.refetch()}
      compact
    />
  ) : (
    <Install
      snippet={snippet.data}
      fw={fw}
      onVersion={(v) => {
        const next = new URLSearchParams(params);
        next.set("version", v);
        setParams(next, { replace: true });
      }}
    />
  );
  const frameworks =
    guides.length > 0 ? (
      <Segmented
        label="How the site is built"
        value={guides.some((g) => g.id === fw) ? fw : (guides[0]?.id ?? "html")}
        options={guides.map((g) => ({ value: g.id, label: g.label }))}
        onChange={setFw}
      />
    ) : null;

  return (
    <div className="flex flex-col gap-7">
      <PageHeader
        crumb={<SitesCrumb tenantId={site.tenant_id} />}
        title={site.name}
        meta={
          site.status !== "active" ? (
            <StatusBadge kind="site" status={site.status} />
          ) : site.in_review ? (
            <StatusBadge kind="site" status="pending" />
          ) : null
        }
        items={[
          <span key="d" className="truncate font-mono text-small" title={site.domain}>
            {site.domain}
          </span>,
          counted !== null && counted > 0 ? (
            <Link
              key="e"
              to={webPath(site.tenant_id, site.id)}
              className="rounded-sm text-fg-2 transition-colors duration-(--vs-duration-press) hover:text-fg focus-ring"
            >
              {formatCount(counted)} events, 30 days
            </Link>
          ) : null,
        ]}
        secondary={
          <>
            {event === "received" ? (
              <Button asChild variant="outline">
                <Link to={webPath(site.tenant_id, site.id)}>Web data</Link>
              </Button>
            ) : null}
            <RowMenu
              name={site.name}
              actions={[
                {
                  label: `Open ${site.domain}`,
                  icon: <ExternalLink aria-hidden />,
                  onSelect: () => window.open(`https://${site.domain}`, "_blank", "noopener"),
                },
                {
                  label: "Settings",
                  icon: <Settings aria-hidden />,
                  onSelect: () => navigate({ hash: "settings" }, { replace: true }),
                },
                canDelete && {
                  label: "Delete site",
                  icon: <Trash2 aria-hidden />,
                  destructive: true,
                  confirm: {
                    title: `Delete the site “${site.name}”?`,
                    description: `Events from ${site.domain} are refused, and all web data of this site is deleted. You cannot undo this.`,
                    confirmLabel: "Delete site",
                    pendingLabel: "Deleting",
                    typed: { value: site.domain, label: `Type ${site.domain} to confirm` },
                    run: async () => {
                      await api.deleteSite(site.tenant_id, site.id);
                      toast.success(`${site.name} was deleted.`);
                      await queryClient.invalidateQueries({
                        queryKey: keys.sites(site.tenant_id),
                      });
                      navigate(`/projects/${site.tenant_id}/sites`);
                    },
                  },
                },
              ]}
            />
          </>
        }
      />
      {site.in_review ? (
        <Notice tone="info" title="In review">
          This domain belonged to a removed project. An operator checks it before the site receives
          traffic.
        </Notice>
      ) : null}
      {site.status_reason ? (
        <Notice tone="info" title="Reason from an operator">
          {site.status_reason}
        </Notice>
      ) : null}

      <div id="setup" className="flex scroll-mt-6 flex-col gap-4">
        <SetupProgress
          steps={steps}
          currentAside={current?.key === "install" ? frameworks : null}
          optional={{
            label: "stellar.toml",
            done: tomlFound,
            result: (
              <>
                <span>{tomlFound ? "Published" : "Optional, not published"}</span>
                {toml ? (
                  <button
                    type="button"
                    aria-expanded={shown === "toml"}
                    onClick={() => toggle("toml")}
                  >
                    {shown === "toml" ? "Hide the check" : "Show the check"}
                  </button>
                ) : null}
                {!tomlFound && docs ? (
                  <a href={`${docs}/onboarding/onchain`}>How to publish it</a>
                ) : null}
              </>
            ),
          }}
        >
          {event === null ? (
            <p className="text-small text-fg-2" aria-live="polite">
              Checking for events
            </p>
          ) : current?.key === "install" ? (
            <div id="install" className="flex min-w-0 scroll-mt-6 flex-col gap-4">
              {installBody}
              <Waiting site={site} />
            </div>
          ) : current?.key === "event" ? (
            <div id="event" className="scroll-mt-6">
              <Refused site={site} day={first.day} month={first.month} />
            </div>
          ) : current?.key === "domain" ? (
            <div id="domain" className="scroll-mt-6">
              <VerifyDomain site={site} current />
            </div>
          ) : null}
        </SetupProgress>

        {/* the optional step while the setup is open: its check on demand */}
        {current && toml ? (
          <div className="flex justify-end">
            <Button
              variant="link"
              size="sm"
              aria-expanded={shown === "toml"}
              onClick={() => toggle("toml")}
            >
              {shown === "toml" ? "Hide the stellar.toml check" : "Show the stellar.toml check"}
            </Button>
          </div>
        ) : null}

        <AnimatePresence initial={false}>
          {open ? (
            <motion.div
              key={open}
              {...fade}
              className="rounded-2xl border border-line bg-raised px-6 py-5 max-sm:px-4"
            >
              {open === "tag" ? (
                <div className="flex min-w-0 flex-col gap-4">
                  {frameworks}
                  {installBody}
                </div>
              ) : open === "record" ? (
                <div id="domain">
                  <VerifyDomain site={site} />
                </div>
              ) : (
                <StellarTomlSummary
                  toml={site.stellar_toml}
                  checkedAt={site.stellar_toml_checked_at}
                  domain={site.domain}
                />
              )}
            </motion.div>
          ) : null}
        </AnimatePresence>
      </div>

      <SiteSettings site={site} />
    </div>
  );
}

// the live line under the tag: the first event is polled until it arrives
function Waiting({ site }: { site: Site }) {
  return (
    <div
      aria-live="polite"
      className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 text-small"
    >
      <span className="flex items-center gap-2.5 text-fg-2">
        <StatusDot tone="info" live />
        Waiting for the first event
      </span>
      <a
        href={`https://${site.domain}`}
        target="_blank"
        rel="noopener noreferrer"
        className="inline-flex max-w-full items-center gap-1 rounded-sm text-fg-2 underline decoration-line-strong underline-offset-[3px] transition-colors duration-(--vs-duration-press) hover:text-fg focus-ring"
      >
        <span className="truncate">Open {site.domain}</span>
        <ExternalLink aria-hidden className="size-3.5 shrink-0" />
      </a>
    </div>
  );
}

// events arrive but are refused: the reasons with their counts and the fix
function Refused({
  site,
  day,
  month,
}: {
  site: Site;
  day: Ingest | undefined;
  month: Ingest | undefined;
}) {
  // the month answer is cached longer than the polled day, so a refusal seen
  // in the last 24 hours is named even before the month catches up
  const monthReasons = rejectionRows(month?.totals ?? {});
  const reasons = (
    monthReasons.length > 0 ? monthReasons : rejectionRows(day?.totals ?? {})
  ).slice(0, 3);
  const period = monthReasons.length > 0 ? "30 days" : "24 hours";
  const originRefused = reasons.some((r) => r.key === "rejected_origin");
  return (
    <div aria-live="polite" className="flex flex-col gap-4">
      <span className="flex items-center gap-2 text-body font-medium text-fg">
        <CircleAlert aria-hidden className="size-4 text-critical" />
        Events arrive, but the service refuses them
      </span>
      <ul className="flex flex-col divide-y divide-line-subtle text-small">
        {reasons.map((r) => (
          <li key={r.key} className="flex flex-col gap-1 py-2 first:pt-0 last:pb-0">
            <span className="flex flex-wrap items-baseline justify-between gap-x-4">
              <span className="text-fg">{r.label}</span>
              <span className="text-fg-2 tabular">
                {formatCount(r.count)} in {period}
              </span>
            </span>
            {outcomeHint(r.key) ? <span className="text-fg-2">{outcomeHint(r.key)}</span> : null}
          </li>
        ))}
      </ul>
      <div className="flex flex-wrap gap-2">
        {originRefused ? (
          <Button asChild variant="outline" size="sm" className="max-lg:h-(--vs-control-touch)">
            <Link to={{ hash: "settings" }} replace>
              Edit allowed origins
            </Link>
          </Button>
        ) : null}
        <Button asChild variant="ghost" size="sm" className="max-lg:h-(--vs-control-touch)">
          <Link to={`${webPath(site.tenant_id, site.id)}#outcomes`}>See every outcome</Link>
        </Button>
      </div>
    </div>
  );
}

// an option of the tag: a checkbox and what it adds
function TagOption({
  label,
  checked,
  onChange,
  children,
}: {
  label: ReactNode;
  checked: boolean;
  onChange: (on: boolean) => void;
  children?: ReactNode;
}) {
  const id = useId();
  return (
    <div className="flex flex-col gap-2">
      <span className="flex items-center gap-2.5">
        <Checkbox id={id} checked={checked} onCheckedChange={(c) => onChange(c === true)} />
        <label htmlFor={id} className="text-small text-fg">
          {label}
        </label>
      </span>
      {children}
    </div>
  );
}

function Install({
  snippet,
  fw,
  onVersion,
}: {
  snippet: Snippet;
  fw: string;
  onVersion: (v: string) => void;
}) {
  const [consent, setConsent] = useState(false);
  const [title, setTitle] = useState(false);
  const pickerId = useId();
  const titleChoice = readsTitleChoice(snippet.version);
  const guides = snippetGuides(snippet, { consent, title });
  const g = guides.find((x) => x.id === fw) ?? guides[0];
  const v = snippet.versions.find((x) => x.version === snippet.version);
  if (!g) return null;
  const npm = g.id === "npm";
  const code = (t: string) => <code className="font-mono text-caption text-fg-2">{t}</code>;

  return (
    <section aria-label="Install the tag" className="flex min-w-0 flex-col gap-4">
      <CodeBlock
        code={g.code}
        copyText={g.copy}
        label={g.where}
        copyLabel={npm ? "Copy command" : "Copy tag"}
        className="min-w-0"
      />
      {g.note ? <p className="max-w-prose text-small text-fg-2">{g.note}</p> : null}

      <Disclosure
        summary="Tag options and version"
        className="bg-transparent text-small"
        summaryClassName="px-0 text-small font-normal text-fg-2 hover:bg-transparent hover:text-fg"
        bodyClassName="flex flex-col gap-4 py-3"
      >
        <TagOption
          label={
            <span className="flex flex-wrap items-baseline gap-x-2">
              <span>Wait for consent before measuring</span>
              {npm ? code("autoTrack: false") : code('data-auto-track="false"')}
            </span>
          }
          checked={consent}
          onChange={setConsent}
        >
          <AnimatePresence initial={false}>
            {consent ? (
              <motion.div {...fade}>
                <CodeBlock
                  code={CONSENT_START}
                  label="When the visitor agrees"
                  copyLabel="Copy call"
                />
              </motion.div>
            ) : null}
          </AnimatePresence>
        </TagOption>
        {titleChoice ? (
          <TagOption
            label={
              <span className="flex flex-wrap items-baseline gap-x-2">
                <span>Send the page title</span>
                {npm ? code("sendTitle: true") : code('data-send-title="true"')}
              </span>
            }
            checked={title}
            onChange={setTitle}
          />
        ) : null}
        <div className="flex flex-wrap items-end gap-x-6 gap-y-3">
          <div className="flex flex-col gap-1">
            <label htmlFor={pickerId} className="text-small font-medium text-fg">
              Tracker version
            </label>
            <NativeSelect
              id={pickerId}
              value={snippet.version}
              onChange={(e) => onVersion(e.target.value)}
            >
              {snippet.versions.map((ver, i) => (
                <option key={ver.version} value={ver.version}>
                  {ver.version}
                  {i === 0 ? " (recommended)" : ""}
                </option>
              ))}
            </NativeSelect>
          </div>
          {v ? (
            <dl className="flex gap-6 pb-1 text-small">
              <div className="flex flex-col">
                <dt className="text-caption text-fg-3">Released</dt>
                <dd className="text-fg-2">{formatDate(v.released)}</dd>
              </div>
              <div className="flex flex-col">
                <dt className="text-caption text-fg-3">Compressed</dt>
                <dd className="text-fg-2 tabular">{formatBytes(v.gzip_bytes)}</dd>
              </div>
              <div className="flex flex-col">
                <dt className="text-caption text-fg-3">Raw</dt>
                <dd className="text-fg-2 tabular">{formatBytes(v.bytes)}</dd>
              </div>
            </dl>
          ) : null}
        </div>
        <dl className="grid gap-x-4 gap-y-2 sm:grid-cols-[6rem_minmax(0,1fr)] sm:items-center">
          <dt className="text-fg-2">Script</dt>
          <dd className="min-w-0">
            <CopyField value={snippet.src} label="script address" />
          </dd>
          <dt className="text-fg-2">Integrity</dt>
          <dd className="min-w-0">
            <CopyField value={snippet.integrity} label="integrity" />
          </dd>
          <dt className="text-fg-2">Site id</dt>
          <dd className="min-w-0">
            <CopyField value={snippet.site_id} label="site id" />
          </dd>
        </dl>
      </Disclosure>
    </section>
  );
}
