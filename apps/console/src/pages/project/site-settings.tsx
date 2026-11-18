import { useMutation, useQuery } from "@tanstack/react-query";
import { Check, Trash2 } from "lucide-react";
import { type FormEvent, useId, useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { api, type SitePatch } from "@/api/endpoints";
import { keys, sitesQuery } from "@/api/queries";
import type { Site } from "@/api/schemas";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { ErrorPanel, FormMessage, Loading } from "@/components/error-panel";
import { TextField } from "@/components/form-field";
import { PageHeader, Panel } from "@/components/panel";
import { StatusPill } from "@/components/status";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { useProject } from "@/pages/project-layout";
import { NotFoundPage } from "@/pages/public/not-found";
import { queryClient } from "@/query-client";
import {
  OriginsField,
  type SiteErrors,
  type SiteValues,
  serverSiteErrors,
  sitePatch,
  siteSetupPath,
  validateSite,
} from "./sites";

// one site's settings: name and allowed origins, campaign parameters and,
// for owners, deleting the site. the domain cannot change.
export function SiteSettingsPage() {
  const { tenant, isOwner, termsCurrent } = useProject();
  const { siteId = "" } = useParams();
  const sites = useQuery(sitesQuery(tenant.id));
  if (sites.isPending) return <Loading label="Loading the site" />;
  if (sites.error) return <ErrorPanel error={sites.error} onRetry={() => sites.refetch()} />;
  const site = sites.data.find((s) => s.id === siteId && s.status !== "removed");
  if (!site) return <NotFoundPage />;
  // a suspended project's sites cannot be changed (contract 5.11); deleting
  // stays possible
  const canEdit = termsCurrent && tenant.status !== "suspended";
  return (
    <div className="flex flex-col gap-8">
      <PageHeader
        title={`${site.name} settings`}
        meta={site.status !== "active" ? <StatusPill status={site.status} /> : null}
        description={<span className="font-mono text-small">{site.domain}</span>}
        actions={
          <Button asChild variant="outline">
            <Link to={siteSetupPath(site)}>Setup</Link>
          </Button>
        }
      />
      <SiteSettings
        site={site}
        canEdit={canEdit}
        canDelete={isOwner && termsCurrent}
        isOwner={isOwner}
      />
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
      navigate(`/projects/${site.tenant_id}/sites`);
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
