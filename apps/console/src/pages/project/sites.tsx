import { useMutation, useQueries, useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { Check, ChevronRight, CircleAlert, Minus, Plus } from "lucide-react";
import { type FormEvent, type ReactNode, useState } from "react";
import { Link, useLocation, useNavigate } from "react-router";
import { ApiError } from "@/api/client";
import { api, type SiteInput, type SitePatch } from "@/api/endpoints";
import { ingestMonthQuery, keys, sitesQuery } from "@/api/queries";
import type { Ingest, Site } from "@/api/schemas";
import { ErrorPanel, FormMessage, Loading, StateBlock } from "@/components/error-panel";
import { TextAreaField, TextField } from "@/components/form-field";
import { Stagger, StaggerItem } from "@/components/motion";
import { Notice } from "@/components/notice";
import { PageHeader, Panel } from "@/components/panel";
import { StatusPill } from "@/components/status";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Table, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { formatCount, formatShareOf } from "@/lib/format";
import { sumRejected } from "@/lib/outcomes";
import * as v from "@/lib/validate";
import { webPath } from "@/lib/web-dashboard";
import { useProject } from "@/pages/project-layout";
import { queryClient } from "@/query-client";

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

// a site's own pages: its setup (install, first event, domain) and its
// settings
export const siteSetupPath = (s: Pick<Site, "id" | "tenant_id">) =>
  `/projects/${s.tenant_id}/sites/${s.id}`;
export const siteSettingsPath = (s: Pick<Site, "id" | "tenant_id">) =>
  `/projects/${s.tenant_id}/sites/${s.id}/settings`;

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
                      to={siteSetupPath(s)}
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
                    {t ? formatShareOf(held, all) : pending}
                  </TableCell>
                  <TableCell>
                    <div className="flex items-center justify-end gap-1">
                      {event === "waiting" || event === "refused" ? (
                        <Button asChild variant="ghost" size="sm" className="text-accent-text">
                          <Link to={siteSetupPath(s)}>Continue setup</Link>
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
                to={siteSetupPath(s)}
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
              if (site) navigate(siteSetupPath(site));
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

export function OriginsField({
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
  const { tenant, termsCurrent } = useProject();
  // a suspended project's sites cannot be created or changed (contract 5.11)
  const canChange = termsCurrent && tenant.status !== "suspended";
  const sites = useQuery(sitesQuery(tenant.id));
  const location = useLocation();
  const navigate = useNavigate();
  const justEnrolled = (location.state as { enrolled?: boolean } | null)?.enrolled === true;

  if (sites.isPending) return <Loading label="Loading sites" />;
  if (sites.error) return <ErrorPanel error={sites.error} onRetry={() => sites.refetch()} />;
  const live = sites.data.filter((s) => s.status !== "removed");

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
                if (site) navigate(siteSetupPath(site));
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
