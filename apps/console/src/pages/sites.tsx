import { useMutation, useQueries, useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import {
  ArrowLeft,
  Check,
  ChevronRight,
  CircleAlert,
  Code2,
  Minus,
  Plus,
  ShieldCheck,
  Trash2,
} from "lucide-react";
import { type FormEvent, type ReactNode, useId, useState } from "react";
import { Link, useLocation, useNavigate, useSearchParams } from "react-router";
import { ApiError } from "@/api/client";
import { api, type SiteInput, type SitePatch } from "@/api/endpoints";
import { ingestMonthQuery, keys, sitesQuery } from "@/api/queries";
import type { Ingest, Site } from "@/api/schemas";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { DbIpAttribution } from "@/components/dbip-attribution";
import { ErrorPanel, FormMessage, Loading, StateBlock } from "@/components/error-panel";
import { StellarTomlSummary, VerificationDetail, VerificationPill } from "@/components/evidence";
import { TextAreaField, TextField } from "@/components/form-field";
import { Stagger, StaggerItem } from "@/components/motion";
import { Notice } from "@/components/notice";
import { PageHeader, Panel } from "@/components/panel";
import { Segmented } from "@/components/segmented";
import { Pill, StatusPill } from "@/components/status";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog } from "@/components/ui/dialog";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  formatShare,
  readWebRange,
  useWebTraffic,
  WEB_RANGES,
  type WebRange,
  WebTraffic,
} from "@/components/web-charts";
import { formatCount } from "@/lib/format";
import { outcomeHint, rejectionRows, sumRejected, sumUnclassified } from "@/lib/outcomes";
import * as v from "@/lib/validate";
import { webPath } from "@/lib/web-dashboard";
import { queryClient } from "@/query-client";
import { useProject } from "./project-layout";

export type SiteValues = { name: string; domain: string; origins: string };
export type SiteErrors = Partial<Record<keyof SiteValues, string>>;

export function parseOrigins(text: string): string[] {
  return text
    .split(/[\s,]+/)
    .map((s) => s.trim())
    .filter((s) => s !== "");
}

export function validateSite(values: SiteValues): SiteErrors {
  const errors: SiteErrors = {};
  const name = v.siteName(values.name);
  if (name) errors.name = v.fieldMessage(name);
  const domain = v.domain(values.domain.trim());
  if (domain) errors.domain = v.fieldMessage(domain);
  const list = parseOrigins(values.origins);
  const found = v.originList(list, domain ? "" : values.domain.trim());
  errors.origins = originMessage(found, list) ?? undefined;
  if (!errors.origins) delete errors.origins;
  return errors;
}

// one message for the origins box, naming the first origin that failed
function originMessage(found: Record<string, string>, list: string[]): string | null {
  if (found.origins) return v.fieldMessage(found.origins);
  for (const [path, code] of Object.entries(found)) {
    const m = /^origins\[(\d+)\]$/.exec(path);
    if (m?.[1] !== undefined) return `${list[Number(m[1])] ?? path}: ${v.fieldMessage(code)}`;
  }
  return null;
}

export function serverSiteErrors(err: unknown, values: SiteValues): SiteErrors {
  if (!(err instanceof ApiError) || err.code !== "validation_failed") return {};
  const out: SiteErrors = {};
  const originsFound: Record<string, string> = {};
  for (const [path, code] of Object.entries(err.fields)) {
    if (path === "name" || path === "domain") out[path] ??= v.fieldMessage(code);
    else if (path === "origins" || path.startsWith("origins[")) originsFound[path] = code;
  }
  const o = originMessage(originsFound, parseOrigins(values.origins));
  if (o) out.origins = o;
  return out;
}

// only the fields that changed: a patch that resends unchanged origins would
// still republish the registry entry for nothing
export function sitePatch(site: Site, values: SiteValues): SitePatch {
  const patch: SitePatch = {};
  const name = values.name.trim();
  if (name !== site.name) patch.name = name;
  const origins = parseOrigins(values.origins);
  if (origins.join("\n") !== site.origins.join("\n")) patch.origins = origins;
  return patch;
}

// ------------------------------------------------------------ setup state

export type FirstEvent = "waiting" | "received" | "refused";

// whether a site's tag works, from the ingest counts the console already
// reads: counted events mean it does; only rejections mean the tag runs but
// its events are refused
export function firstEvent(...answers: (Ingest | undefined)[]): FirstEvent {
  let counted = 0;
  let rejected = 0;
  for (const a of answers) {
    if (!a) continue;
    const t = a.totals;
    counted += (t.accepted ?? 0) + (t.quarantined ?? 0) + (t.bot ?? 0);
    rejected += sumRejected(t);
  }
  return counted > 0 ? "received" : rejected > 0 ? "refused" : "waiting";
}

// setup is three steps: the site is added, its first event arrives, its
// domain is verified
export function setupDone(site: Site, event: FirstEvent | null): number {
  return 1 + (event === "received" ? 1 : 0) + (site.verification === "verified" ? 1 : 0);
}

export const snippetPath = (s: Pick<Site, "id" | "tenant_id">) =>
  `/projects/${s.tenant_id}/sites/${s.id}/snippet`;
export const verifyPath = (s: Pick<Site, "id" | "tenant_id">) =>
  `/projects/${s.tenant_id}/sites/${s.id}/verify`;
export const sitePath = (s: Pick<Site, "id" | "tenant_id">, tab?: "settings" | "setup") =>
  `/projects/${s.tenant_id}/sites?site=${s.id}${tab ? `&tab=${tab}` : ""}`;

type MarkState = "ok" | "wait" | "warn" | "none";

function Mark({ state, children }: { state: MarkState; children: ReactNode }) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 text-caption font-normal",
        state === "none" ? "text-fg-3" : "text-fg-2",
      )}
    >
      {state === "ok" ? (
        <Check aria-hidden className="size-3 text-success" strokeWidth={2.6} />
      ) : state === "wait" ? (
        <span aria-hidden className="relative inline-flex size-3 items-center justify-center">
          <span className="absolute inset-0.5 animate-live rounded-full bg-info" />
          <span className="size-1.5 rounded-full bg-info" />
        </span>
      ) : state === "warn" ? (
        <CircleAlert aria-hidden className="size-3 text-warning" />
      ) : (
        <Minus aria-hidden className="size-3" />
      )}
      {children}
    </span>
  );
}

// the three setup marks of a site, always as icon plus word
export function SetupMarks({ site, event }: { site: Site; event: FirstEvent | null }) {
  return (
    <span className="flex flex-wrap items-center gap-x-3 gap-y-1">
      {event === null ? (
        <Mark state="none">Checking</Mark>
      ) : event === "received" ? (
        <Mark state="ok">Installed</Mark>
      ) : event === "refused" ? (
        <Mark state="warn">Refused</Mark>
      ) : (
        <Mark state="wait">Waiting</Mark>
      )}
      <Mark
        state={
          site.verification === "verified"
            ? "ok"
            : site.verification === "unverified"
              ? "warn"
              : "none"
        }
      >
        Domain
      </Mark>
      <Mark state={site.stellar_toml?.status === "found" ? "ok" : "none"}>stellar.toml</Mark>
    </span>
  );
}

function phoneSummary(site: Site, event: FirstEvent | null): string {
  if (event === null) return "Checking";
  if (event === "refused") return "Events are refused";
  if (event === "waiting") return "Waiting for first event";
  if (site.verification !== "verified") return "Installed, domain not verified";
  return site.stellar_toml?.status === "found" ? "Verified, stellar.toml found" : "Verified";
}

// ------------------------------------------------------------ sites table

// the project's sites with how far each one is set up and its last 30 days.
// rows stack on a phone.
export function SitesTable({ sites, actions }: { sites: Site[]; actions?: ReactNode }) {
  const month = useQueries({ queries: sites.map((s) => ingestMonthQuery(s.id)) });
  return (
    <Panel title="Sites" actions={actions} flush className="overflow-hidden">
      <div className="max-md:hidden">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Site</TableHead>
              <TableHead>Setup</TableHead>
              <TableHead className="text-right">Accepted, 30d</TableHead>
              <TableHead className="text-right">Held back</TableHead>
              <TableHead>
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <Stagger as="tbody">
            {sites.map((s, i) => {
              const q = month[i];
              const event = q?.data ? firstEvent(q.data) : null;
              const t = q?.data?.totals;
              const all = t ? Object.values(t).reduce((a, b) => a + b, 0) : 0;
              const held = t ? (t.quarantined ?? 0) + (t.bot ?? 0) + sumRejected(t) : 0;
              const pending = q?.error ? "n/a" : "...";
              return (
                <StaggerItem
                  as="tr"
                  key={s.id}
                  className="transition-colors duration-(--vs-duration-press) ease-standard hover:bg-hover"
                >
                  <TableCell>
                    <Link
                      to={sitePath(s)}
                      className="group/site flex min-w-0 flex-col rounded-sm focus-ring"
                    >
                      <span className="flex items-center gap-2 font-medium text-fg group-hover/site:underline">
                        {s.name}
                        {s.status !== "active" ? <StatusPill status={s.status} /> : null}
                      </span>
                      <span className="truncate text-caption font-normal text-fg-3">
                        {s.domain}
                      </span>
                    </Link>
                  </TableCell>
                  <TableCell>
                    <SetupMarks site={s} event={event} />
                  </TableCell>
                  <TableCell className="text-right font-mono text-fg">
                    {t ? formatCount(t.accepted ?? 0) : pending}
                  </TableCell>
                  <TableCell className="text-right font-mono text-fg-2">
                    {t ? formatShare(held, all) : pending}
                  </TableCell>
                  <TableCell>
                    <div className="flex items-center justify-end gap-1">
                      {event === "waiting" || event === "refused" ? (
                        <Button asChild variant="ghost" size="sm" className="text-accent-text">
                          <Link to={snippetPath(s)}>Continue setup</Link>
                        </Button>
                      ) : null}
                      <Button asChild variant="ghost" size="sm">
                        <Link
                          to={webPath(s.tenant_id, s.id)}
                          aria-label={`Web analytics of ${s.name}`}
                        >
                          Analytics
                        </Link>
                      </Button>
                    </div>
                  </TableCell>
                </StaggerItem>
              );
            })}
          </Stagger>
        </Table>
      </div>
      <Stagger as="ul" className="flex flex-col md:hidden">
        {sites.map((s, i) => {
          const q = month[i];
          const event = q?.data ? firstEvent(q.data) : null;
          return (
            <StaggerItem as="li" key={s.id} className="border-t border-line-subtle">
              <Link
                to={event === "received" ? sitePath(s) : snippetPath(s)}
                className="flex min-h-(--vs-control-touch) items-center gap-3 px-4 py-3 transition-colors duration-(--vs-duration-press) hover:bg-hover focus-ring"
              >
                <span className="flex min-w-0 flex-1 flex-col gap-1">
                  <span className="truncate text-body font-medium text-fg">{s.domain}</span>
                  <span className="truncate text-caption font-normal text-fg-2">
                    {phoneSummary(s, event)}
                  </span>
                </span>
                <span className="font-mono text-body text-fg">
                  {q?.data ? formatCount(q.data.totals.accepted ?? 0) : ""}
                </span>
                <ChevronRight aria-hidden className="size-4 text-fg-3" />
              </Link>
            </StaggerItem>
          );
        })}
      </Stagger>
    </Panel>
  );
}

// ------------------------------------------------------------ add a site

export function AddSiteForm({
  tenantId,
  onDone,
  onCancel,
  cancellable,
  submitLabel = "Add and set up",
}: {
  tenantId: string;
  onDone: (site?: Site) => void;
  onCancel?: () => void;
  cancellable: boolean;
  submitLabel?: string;
}) {
  const [values, setValues] = useState<SiteValues>({ name: "", domain: "", origins: "" });
  const [errors, setErrors] = useState<SiteErrors>({});
  const create = useMutation({
    mutationFn: (body: SiteInput) => api.createSite(tenantId, body),
    onSuccess: async (site) => {
      await queryClient.invalidateQueries({ queryKey: keys.sites(tenantId) });
      onDone(site);
    },
    onError: (err) => setErrors(serverSiteErrors(err, values)),
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    const found = validateSite(values);
    setErrors(found);
    if (Object.keys(found).length > 0) return;
    create.mutate({
      name: values.name.trim(),
      domain: values.domain.trim(),
      origins: parseOrigins(values.origins),
    });
  }

  const domain = values.domain.trim();
  const suggestion = domain && !v.domain(domain) ? `https://${domain}` : null;

  return (
    <form noValidate onSubmit={submit} aria-label="Add a site" className="flex flex-col gap-4">
      <TextField
        label="Name"
        name="name"
        placeholder="Marketing site"
        maxLength={100}
        value={values.name}
        onChange={(e) => setValues((s) => ({ ...s, name: e.target.value }))}
        error={errors.name}
      />
      <TextField
        label="Domain"
        name="domain"
        placeholder="example.org"
        hint="Cannot be changed later."
        autoCapitalize="none"
        spellCheck={false}
        className="font-mono"
        value={values.domain}
        onChange={(e) => setValues((s) => ({ ...s, domain: e.target.value.toLowerCase() }))}
        error={errors.domain}
      />
      <OriginsField
        value={values.origins}
        onChange={(origins) => setValues((s) => ({ ...s, origins }))}
        error={errors.origins}
        suggestion={values.origins.trim() === "" ? suggestion : null}
      />
      <FormMessage
        error={
          create.error && Object.keys(serverSiteErrors(create.error, values)).length === 0
            ? create.error
            : null
        }
      />
      <div className="flex flex-wrap items-center justify-end gap-2 border-t border-line pt-4">
        {cancellable ? (
          <Button
            type="button"
            variant="outline"
            className="max-lg:h-(--vs-control-touch)"
            onClick={onCancel ?? (() => onDone())}
          >
            Cancel
          </Button>
        ) : null}
        <Button type="submit" pending={create.isPending} className="max-lg:h-(--vs-control-touch)">
          {create.isPending ? "Adding" : submitLabel}
        </Button>
      </div>
    </form>
  );
}

// add a site from anywhere in the project; the next screen is its setup
export function AddSiteDialog({
  tenantId,
  open,
  onOpenChange,
}: {
  tenantId: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const navigate = useNavigate();
  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title="Add a site"
      description="One site per website. The next screen walks you through the script and verification."
    >
      {open ? (
        <div className="pb-4">
          <AddSiteForm
            tenantId={tenantId}
            cancellable
            onCancel={() => onOpenChange(false)}
            onDone={(site) => {
              onOpenChange(false);
              if (site) navigate(snippetPath(site));
            }}
          />
        </div>
      ) : null}
    </Dialog>
  );
}

export function AddSiteButton({
  tenantId,
  variant = "outline",
}: {
  tenantId: string;
  variant?: "outline" | "default";
}) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button
        variant={variant}
        size={variant === "default" ? "default" : "sm"}
        onClick={() => setOpen(true)}
        className="max-lg:h-(--vs-control-touch)"
      >
        <Plus aria-hidden />
        Add site
      </Button>
      <AddSiteDialog tenantId={tenantId} open={open} onOpenChange={setOpen} />
    </>
  );
}

function OriginsField({
  value,
  onChange,
  error,
  suggestion,
}: {
  value: string;
  onChange: (v: string) => void;
  error?: string;
  suggestion: string | null;
}) {
  return (
    <div className="flex flex-col items-start gap-2">
      <div className="w-full">
        <TextAreaField
          label="Allowed origins"
          name="origins"
          rows={3}
          className="font-mono text-small"
          autoCapitalize="none"
          spellCheck={false}
          placeholder="https://example.org"
          hint="One per line. Events are accepted only from pages on these origins: the domain or its subdomains over https, or http://localhost for testing."
          value={value}
          onChange={(e) => onChange(e.target.value)}
          error={error}
        />
      </div>
      {suggestion ? (
        <Button
          type="button"
          variant="outline"
          size="xs"
          className="animate-fade-in font-mono max-lg:h-(--vs-control-touch)"
          onClick={() => onChange(suggestion)}
        >
          Use {suggestion}
        </Button>
      ) : null}
    </div>
  );
}

// ------------------------------------------------------------ the page

export function SitesPage() {
  const { tenant, isOwner, termsCurrent } = useProject();
  // a suspended project's sites cannot be created or changed (contract 5.11);
  // deleting stays possible
  const canChange = termsCurrent && tenant.status !== "suspended";
  const sites = useQuery(sitesQuery(tenant.id));
  const location = useLocation();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const justEnrolled = (location.state as { enrolled?: boolean } | null)?.enrolled === true;
  const selected = params.get("site");

  if (sites.isPending) return <Loading label="Loading sites" />;
  if (sites.error) return <ErrorPanel error={sites.error} onRetry={() => sites.refetch()} />;
  const live = sites.data.filter((s) => s.status !== "removed");

  if (selected) {
    const site = live.find((s) => s.id === selected);
    if (!site)
      return (
        <StateBlock
          kind="empty"
          title="This site is not in the project"
          action={
            <Button asChild variant="outline">
              <Link to={`/projects/${tenant.id}`}>Back to the overview</Link>
            </Button>
          }
        >
          The site was deleted, or it belongs to another project.
        </StateBlock>
      );
    return (
      <SitePage
        key={site.id}
        site={site}
        canEdit={canChange}
        canDelete={isOwner && termsCurrent}
        isOwner={isOwner}
      />
    );
  }

  if (live.length === 0) {
    return (
      <div className="flex max-w-dialog flex-col gap-6">
        <PageHeader
          title="Add your first site"
          description="Name the website you want to measure. The next screen walks you through the script tag and domain verification."
        />
        {justEnrolled ? (
          <Notice tone="success" title="Project enrolled">
            Add its website to get the tracking snippet.
          </Notice>
        ) : null}
        {canChange ? (
          <Panel>
            <AddSiteForm
              tenantId={tenant.id}
              cancellable={false}
              onDone={(site) => {
                if (site) navigate(snippetPath(site));
              }}
            />
          </Panel>
        ) : (
          <StateBlock kind="empty" title="No sites yet">
            Sites cannot be added while the project is suspended or its current terms are not
            accepted.
          </StateBlock>
        )}
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Sites"
        description="Each website the project measures, how far it is set up and its last 30 days."
      />
      <SitesTable
        sites={live}
        actions={canChange ? <AddSiteButton tenantId={tenant.id} /> : null}
      />
    </div>
  );
}

type SiteTab = "traffic" | "setup" | "settings";

function SitePage({
  site,
  canEdit,
  canDelete,
  isOwner,
}: {
  site: Site;
  canEdit: boolean;
  canDelete: boolean;
  isOwner: boolean;
}) {
  const [params, setParams] = useSearchParams();
  const raw = params.get("tab");
  const tab: SiteTab = raw === "settings" ? "settings" : raw === "setup" ? "setup" : "traffic";
  const range = readWebRange(params.get("range"));
  const set = (k: string, val: string | null) => {
    const next = new URLSearchParams(params);
    if (val === null) next.delete(k);
    else next.set(k, val);
    setParams(next, { replace: true });
  };

  return (
    <div className="flex flex-col gap-5">
      <Link
        to={`/projects/${site.tenant_id}`}
        className="inline-flex w-fit items-center gap-1 rounded-sm text-small text-fg-2 transition-colors duration-(--vs-duration-press) hover:text-fg focus-ring"
      >
        <ArrowLeft aria-hidden className="size-3.5" />
        Overview
      </Link>
      <PageHeader
        title={site.name}
        meta={site.status !== "active" ? <StatusPill status={site.status} /> : null}
        description={
          <span className="flex flex-wrap items-center gap-2">
            <span className="font-mono text-small text-fg-2">{site.domain}</span>
            <VerificationPill verification={site.verification} />
            {site.stellar_toml?.status === "found" ? (
              <Pill tone="success">stellar.toml found</Pill>
            ) : null}
          </span>
        }
        actions={
          <>
            {tab === "traffic" ? (
              <Segmented
                label="Range"
                value={range}
                options={WEB_RANGES}
                onChange={(r) => set("range", r)}
                hint={range === "24h" ? "per hour" : "per day"}
              />
            ) : null}
            {site.quarantine_website_id ? (
              <Button asChild variant="outline">
                <Link to={`${webPath(site.tenant_id, site.id)}&band=quarantine`}>
                  Quarantined traffic
                </Link>
              </Button>
            ) : null}
            <Button asChild>
              <Link to={webPath(site.tenant_id, site.id)}>Web analytics</Link>
            </Button>
          </>
        }
      />

      {site.in_review ? (
        <Notice tone="warning" title="In review">
          This domain belonged to a removed project, so an operator checks it before it receives
          traffic.
        </Notice>
      ) : null}
      {site.status_reason ? (
        <Notice tone="info" title="Reason given by an operator">
          {site.status_reason}
        </Notice>
      ) : null}

      <Tabs
        value={tab}
        onValueChange={(t) => set("tab", t === "traffic" ? null : t)}
        className="gap-5"
      >
        <TabsList variant="line" aria-label="Site sections" className="overflow-x-auto">
          <TabsTrigger value="traffic">Traffic</TabsTrigger>
          <TabsTrigger value="setup">Setup</TabsTrigger>
          <TabsTrigger value="settings">Settings</TabsTrigger>
        </TabsList>
        <TabsContent value="traffic">
          <SiteTraffic site={site} range={range} />
        </TabsContent>
        <TabsContent value="setup">
          <SiteSetupSummary site={site} />
        </TabsContent>
        <TabsContent value="settings">
          <SiteSettings site={site} canEdit={canEdit} canDelete={canDelete} isOwner={isOwner} />
        </TabsContent>
      </Tabs>
    </div>
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

function SiteSetupSummary({ site }: { site: Site }) {
  const month = useQuery(ingestMonthQuery(site.id));
  const event = month.data ? firstEvent(month.data) : null;
  return (
    <div className="grid gap-4 lg:grid-cols-2">
      <Panel
        title="Tracker"
        description="The script tag on the site's pages"
        actions={
          <Button asChild variant="outline" size="sm">
            <Link to={snippetPath(site)}>
              <Code2 aria-hidden />
              Snippet
            </Link>
          </Button>
        }
      >
        <div className="flex flex-col gap-2 text-small">
          <SetupMarks site={site} event={event} />
          <p className="text-fg-2">
            {event === null
              ? "Checking the last 30 days."
              : event === "received"
                ? "Events from this site were counted in the last 30 days."
                : event === "refused"
                  ? "Events arrive, but they are refused. The setup page names the reasons."
                  : "No events counted in the last 30 days."}
          </p>
        </div>
      </Panel>
      <Panel
        title="Domain"
        description="Proof that the project controls the domain"
        actions={
          <Button asChild variant="outline" size="sm">
            <Link to={verifyPath(site)}>
              <ShieldCheck aria-hidden />
              Verify domain
            </Link>
          </Button>
        }
      >
        <VerificationDetail
          verification={site.verification}
          verifyMethod={site.verify_method}
          verifiedAt={site.verified_at}
          lastVerifyAt={site.last_verify_at}
          lastVerifyError={site.last_verify_error}
        />
      </Panel>
      <Panel className="lg:col-span-2">
        <StellarTomlSummary
          toml={site.stellar_toml}
          checkedAt={site.stellar_toml_checked_at}
          domain={site.domain}
        />
      </Panel>
    </div>
  );
}

function SiteSettings({
  site,
  canEdit,
  canDelete,
  isOwner,
}: {
  site: Site;
  canEdit: boolean;
  canDelete: boolean;
  isOwner: boolean;
}) {
  return (
    <div className="flex max-w-content flex-col gap-4">
      <Panel title="Name and allowed origins" description="The domain cannot be changed.">
        {canEdit ? (
          <EditSiteForm site={site} />
        ) : (
          <dl className="flex flex-col gap-3 text-small">
            <div className="flex flex-col gap-1">
              <dt className="text-caption text-fg-3">Name</dt>
              <dd className="text-fg">{site.name}</dd>
            </div>
            <div className="flex flex-col gap-1">
              <dt className="text-caption text-fg-3">Allowed origins</dt>
              <dd>
                <ul aria-label="Allowed origins" className="flex flex-wrap gap-1">
                  {site.origins.map((o) => (
                    <li
                      key={o}
                      className="rounded-sm border border-line bg-sunken px-2 py-1 font-mono text-caption font-normal text-fg"
                    >
                      {o}
                    </li>
                  ))}
                </ul>
              </dd>
            </div>
          </dl>
        )}
      </Panel>
      <Panel title="Campaign parameters">
        <CampaignParams site={site} canEdit={canEdit} isOwner={isOwner} />
      </Panel>
      {canDelete ? (
        <Panel
          title="Delete site"
          description="Stops collection and deletes both web dashboards of this site with all their data."
          actions={<DeleteSite site={site} />}
          className="border-critical-line"
        />
      ) : null}
    </div>
  );
}

function EditSiteForm({ site }: { site: Site }) {
  const initial = { name: site.name, domain: site.domain, origins: site.origins.join("\n") };
  const [values, setValues] = useState<SiteValues>(initial);
  const [errors, setErrors] = useState<SiteErrors>({});
  const [saved, setSaved] = useState(false);
  const save = useMutation({
    mutationFn: (patch: SitePatch) => api.updateSite(site.tenant_id, site.id, patch),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: keys.sites(site.tenant_id) });
      setSaved(true);
    },
    onError: (err) => setErrors(serverSiteErrors(err, values)),
  });
  const changed = Object.keys(sitePatch(site, values)).length > 0;
  const edit = (next: Partial<SiteValues>) => {
    setSaved(false);
    setValues((s) => ({ ...s, ...next }));
  };

  function submit(e: FormEvent) {
    e.preventDefault();
    const found = validateSite(values);
    setErrors(found);
    if (Object.keys(found).length > 0) return;
    const patch = sitePatch(site, values);
    if (Object.keys(patch).length === 0) return;
    save.mutate(patch);
  }

  return (
    <form
      noValidate
      onSubmit={submit}
      aria-label={`Edit ${site.name}`}
      className="flex flex-col gap-4"
    >
      <TextField
        label="Name"
        name="name"
        maxLength={100}
        value={values.name}
        onChange={(e) => edit({ name: e.target.value })}
        error={errors.name}
      />
      <OriginsField
        value={values.origins}
        onChange={(origins) => edit({ origins })}
        error={errors.origins}
        suggestion={null}
      />
      <p className="text-caption font-normal text-fg-2">
        New origins take effect at the collection endpoint within a minute.
      </p>
      <FormMessage
        error={
          save.error && Object.keys(serverSiteErrors(save.error, values)).length === 0
            ? save.error
            : null
        }
      />
      <div className="flex flex-wrap items-center gap-3">
        <Button type="submit" size="sm" pending={save.isPending} disabled={!changed}>
          {save.isPending ? "Saving" : "Save changes"}
        </Button>
        {changed ? (
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => {
              setValues(initial);
              setErrors({});
              save.reset();
            }}
          >
            Cancel
          </Button>
        ) : null}
        <span role="status" className="flex items-center gap-1 text-small text-fg-2">
          {saved && !changed ? (
            <>
              <Check aria-hidden className="size-3.5 text-success" />
              Saved
            </>
          ) : null}
        </span>
      </div>
    </form>
  );
}

// utm collection (contract 3.1 collect_utm, 4.3.4): off unless an owner turns
// it on, because campaign parameters can carry identifiers and so change the
// site's privacy position. saved at once, because it is one switch and not
// part of the edit form.
export function CampaignParams({
  site,
  canEdit,
  isOwner,
}: {
  site: Site;
  canEdit: boolean;
  isOwner: boolean;
}) {
  const id = useId();
  const save = useMutation({
    mutationFn: (on: boolean) => api.updateSite(site.tenant_id, site.id, { collect_utm: on }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: keys.sites(site.tenant_id) });
    },
  });
  const checked =
    save.isPending && save.variables !== undefined ? save.variables : site.collect_utm;
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-start gap-3">
        <Checkbox
          id={id}
          checked={checked}
          disabled={!canEdit || !isOwner || save.isPending}
          onCheckedChange={(c) => save.mutate(c === true)}
          aria-describedby={`${id}-d`}
          className="mt-1"
        />
        <label htmlFor={id} className="text-body text-fg">
          Keep campaign parameters (<code className="font-mono text-small">utm_*</code> and{" "}
          <code className="font-mono text-small">ref</code>) in page addresses
        </label>
      </div>
      <p id={`${id}-d`} className="pl-6 text-small text-fg-2">
        Off by default: every query parameter is dropped before storage. Turning this on keeps the
        campaign parameters, which France's CNIL recommends not collecting for analytics run without
        consent. It applies to new events within a minute.
        {canEdit && !isOwner ? " Only an owner of the project can change it." : null}
      </p>
      <FormMessage error={save.error} />
    </div>
  );
}

function DeleteSite({ site }: { site: Site }) {
  const [open, setOpen] = useState(false);
  const navigate = useNavigate();
  const remove = useMutation({
    mutationFn: () => api.deleteSite(site.tenant_id, site.id),
    onSuccess: async () => {
      setOpen(false);
      await queryClient.invalidateQueries({ queryKey: keys.sites(site.tenant_id) });
      navigate(`/projects/${site.tenant_id}`);
    },
  });
  return (
    <>
      <Button variant="destructive" size="sm" onClick={() => setOpen(true)}>
        <Trash2 aria-hidden />
        Delete
      </Button>
      <ConfirmDialog
        open={open}
        onOpenChange={(o) => {
          setOpen(o);
          if (!o) remove.reset();
        }}
        title={`Delete ${site.name}?`}
        description={`Events from ${site.domain} stop being accepted, and both web dashboards of this site are deleted with all their data. This cannot be undone.`}
        cancelLabel="Keep site"
        confirmLabel="Delete site"
        pendingLabel="Deleting"
        pending={remove.isPending}
        error={remove.error}
        onConfirm={() => remove.mutate()}
      />
    </>
  );
}
