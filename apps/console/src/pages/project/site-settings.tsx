import { useMutation, useQuery } from "@tanstack/react-query";
import { ListChecks, Trash2 } from "lucide-react";
import { type FormEvent, type ReactNode, useId, useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { api, type SitePatch } from "@/api/endpoints";
import { keys, sitesQuery } from "@/api/queries";
import type { Site } from "@/api/schemas";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { FormMessage } from "@/components/error-panel";
import { TextField } from "@/components/form-field";
import { PageHeader } from "@/components/page-header";
import { ErrorState } from "@/components/states";
import { StatusBadge } from "@/components/status-badge";
import { toast } from "@/components/toast";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { siteSetupPath } from "@/lib/site-setup";
import { useProject } from "@/pages/project-layout";
import { NotFoundPage } from "@/pages/public/not-found";
import { queryClient } from "@/query-client";
import { SitePageLoading, SitesBackLink } from "./page-frame";
import {
  OriginsField,
  type SiteErrors,
  type SiteValues,
  serverSiteErrors,
  sitePatch,
  validateSite,
} from "./sites";
// one site's settings: name and allowed origins, campaign parameters and,
// for owners, deleting the site. the domain cannot change.
export function SiteSettingsPage() {
  const { tenant, isOwner, termsCurrent } = useProject();
  const { siteId = "" } = useParams();
  const sites = useQuery(sitesQuery(tenant.id));
  const site = sites.data?.find((s) => s.id === siteId && s.status !== "removed");
  if (sites.data && !site) return <NotFoundPage />;
  if (sites.error)
    return (
      <div className="flex flex-col gap-6">
        <SitesBackLink tenantId={tenant.id} />
        <PageHeader title="Site settings" />
        <ErrorState
          title="The site did not load"
          error={sites.error}
          onRetry={() => sites.refetch()}
        />
      </div>
    );
  if (!site) return <SitePageLoading tenantId={tenant.id} page="settings" />;
  // a suspended project's sites cannot be changed (contract 5.11); deleting
  // stays possible
  const canEdit = termsCurrent && tenant.status !== "suspended";
  return (
    <div className="flex flex-col gap-6">
      <SitesBackLink tenantId={tenant.id} />
      <PageHeader
        title={`${site.name} settings`}
        meta={site.status !== "active" ? <StatusBadge kind="site" status={site.status} /> : null}
        description={
          <span className="block truncate font-mono text-small" title={site.domain}>
            {site.domain}
          </span>
        }
        secondary={
          <Button asChild variant="outline">
            <Link to={siteSetupPath(site)}>
              <ListChecks aria-hidden />
              Setup
            </Link>
          </Button>
        }
      />
      <div className="flex max-w-prose flex-col gap-8">
        <Section title="Name and allowed origins" description="You cannot change the domain.">
          {canEdit ? <EditSiteForm site={site} /> : <ReadOnly site={site} />}
        </Section>
        <Section title="Campaign parameters">
          <CampaignParams site={site} canEdit={canEdit} isOwner={isOwner} />
        </Section>
        {isOwner && termsCurrent ? <DangerZone site={site} /> : null}
      </div>
    </div>
  );
}

function Section({
  title,
  description,
  children,
}: {
  title: string;
  description?: string;
  children: ReactNode;
}) {
  const id = useId();
  return (
    <section aria-labelledby={id} className="flex flex-col gap-4">
      <div className="flex flex-col gap-1">
        <h2 id={id} className="text-title text-fg">
          {title}
        </h2>
        {description ? <p className="text-small text-fg-2">{description}</p> : null}
      </div>
      <div className="rounded-lg border border-line bg-raised p-5 max-sm:p-4">{children}</div>
    </section>
  );
}

function ReadOnly({ site }: { site: Site }) {
  return (
    <dl className="flex flex-col gap-3 text-small">
      <div className="flex flex-col gap-1">
        <dt className="text-caption text-fg-2">Name</dt>
        <dd className="text-fg">{site.name}</dd>
      </div>
      <div className="flex min-w-0 flex-col gap-1">
        <dt className="text-caption text-fg-2">Allowed origins</dt>
        <dd>
          <ul aria-label="Allowed origins" className="flex flex-col gap-1">
            {site.origins.map((o) => (
              <li key={o} className="truncate font-mono text-fg" title={o}>
                {o}
              </li>
            ))}
          </ul>
        </dd>
      </div>
    </dl>
  );
}

function EditSiteForm({ site }: { site: Site }) {
  const initial = { name: site.name, domain: site.domain, origins: site.origins.join("\n") };
  const [values, setValues] = useState<SiteValues>(initial);
  const [errors, setErrors] = useState<SiteErrors>({});
  const save = useMutation({
    mutationFn: (patch: SitePatch) => api.updateSite(site.tenant_id, site.id, patch),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: keys.sites(site.tenant_id) });
      toast.success("Changes saved. New origins apply within a minute.");
    },
    onError: (err) => setErrors(serverSiteErrors(err, values)),
  });
  const changed = Object.keys(sitePatch(site, values)).length > 0;
  const edit = (next: Partial<SiteValues>) => setValues((s) => ({ ...s, ...next }));

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
      <FormMessage
        error={
          save.error && Object.keys(serverSiteErrors(save.error, values)).length === 0
            ? save.error
            : null
        }
      />
      <div className="flex flex-wrap items-center gap-3">
        <Button
          type="submit"
          pending={save.isPending}
          disabled={!changed}
          className="max-lg:h-(--vs-control-touch)"
        >
          {save.isPending ? "Saving" : "Save changes"}
        </Button>
        {changed ? (
          <Button
            type="button"
            variant="ghost"
            className="max-lg:h-(--vs-control-touch)"
            onClick={() => {
              setValues(initial);
              setErrors({});
              save.reset();
            }}
          >
            Cancel
          </Button>
        ) : null}
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
    onSuccess: async (_, on) => {
      toast.success(on ? "Campaign parameters are kept." : "Campaign parameters are dropped.");
      await queryClient.invalidateQueries({ queryKey: keys.sites(site.tenant_id) });
    },
  });
  const checked =
    save.isPending && save.variables !== undefined ? save.variables : site.collect_utm;
  return (
    <div className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-2">
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
      <p id={`${id}-d`} className="col-start-2 text-small text-fg-2">
        Off by default: the service drops all query parameters. These parameters can identify a
        person, so analytics without consent should not keep them. A change applies within a minute.
        {canEdit && !isOwner ? " Only an owner of the project can change it." : null}
      </p>
      <FormMessage error={save.error} className="col-start-2" />
    </div>
  );
}

function DangerZone({ site }: { site: Site }) {
  const [open, setOpen] = useState(false);
  const navigate = useNavigate();
  const remove = useMutation({
    mutationFn: () => api.deleteSite(site.tenant_id, site.id),
    onSuccess: async () => {
      setOpen(false);
      toast.success(`${site.name} was deleted.`);
      await queryClient.invalidateQueries({ queryKey: keys.sites(site.tenant_id) });
      navigate(`/projects/${site.tenant_id}/sites`);
    },
  });
  return (
    <section aria-labelledby="danger-h" className="flex flex-col gap-4">
      <h2 id="danger-h" className="text-title text-fg">
        Danger zone
      </h2>
      <div className="flex flex-wrap items-center justify-between gap-4 rounded-lg border border-critical-line bg-raised p-5 max-sm:p-4">
        <div className="flex min-w-0 flex-col gap-1">
          <span className="text-body font-medium text-fg">Delete this site</span>
          <span className="text-small text-fg-2">
            The site stops collecting. Its web data is deleted.
          </span>
        </div>
        <Button
          variant="destructive-outline"
          className="max-lg:h-(--vs-control-touch)"
          onClick={() => {
            remove.reset();
            setOpen(true);
          }}
        >
          <Trash2 aria-hidden />
          Delete site
        </Button>
      </div>
      <ConfirmDialog
        open={open}
        onOpenChange={setOpen}
        title={`Delete the site “${site.name}”?`}
        description={`Events from ${site.domain} are refused, and all web data of this site is deleted. You cannot undo this.`}
        cancelLabel="Keep site"
        confirmLabel="Delete site"
        pendingLabel="Deleting"
        pending={remove.isPending}
        error={remove.error}
        typed={{ value: site.domain, label: `Type ${site.domain} to confirm` }}
        onConfirm={() => remove.mutate()}
      />
    </section>
  );
}
