import { useMutation } from "@tanstack/react-query";
import { X } from "lucide-react";
import { type FormEvent, type KeyboardEvent, type ReactNode, useId, useState } from "react";
import { Navigate, useNavigate, useParams } from "react-router";
import { api, type SitePatch } from "@/api/endpoints";
import { keys } from "@/api/queries";
import type { Site } from "@/api/schemas";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { FormMessage } from "@/components/error-panel";
import { toast } from "@/components/toast";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Tooltip } from "@/components/ui/tooltip";
import { useProject } from "@/pages/project-layout";
import { queryClient } from "@/query-client";
import {
  parseOrigins,
  type SiteErrors,
  type SiteValues,
  serverSiteErrors,
  sitePatch,
  validateSite,
} from "./sites";

// the old settings address of a site: settings now live on the site's page
export function SiteSettingsPage() {
  const { tenant } = useProject();
  const { siteId = "" } = useParams();
  return <Navigate to={`/projects/${tenant.id}/sites/${siteId}#settings`} replace />;
}

// one site's settings as label-left rows on its page: name, allowed origins,
// campaign parameters and, for owners, deleting the site. the domain cannot
// change. Save appears once the name or the origins change.
export function SiteSettings({ site }: { site: Site }) {
  const { tenant, isOwner, termsCurrent, readOnly } = useProject();
  // a suspended project's sites cannot be changed (contract 5.11); deleting
  // stays possible. a reader sees the settings as text
  const canEdit = termsCurrent && tenant.status !== "suspended" && !readOnly;
  const id = useId();
  return (
    <section id="settings" aria-labelledby={id} className="flex scroll-mt-6 flex-col">
      <h2 id={id} className="pb-3 text-title leading-5 font-semibold text-fg">
        Settings
      </h2>
      {canEdit ? (
        <EditSiteForm site={site} />
      ) : (
        <>
          <Row label="Name">
            <span className="text-body text-fg">{site.name}</span>
          </Row>
          <Row label="Allowed origins">
            <ul aria-label="Allowed origins" className="flex flex-col gap-1">
              {site.origins.map((o) => (
                <li key={o} className="truncate font-mono text-small text-fg" title={o}>
                  {o}
                </li>
              ))}
            </ul>
          </Row>
        </>
      )}
      <Row label="Campaign parameters">
        <CampaignParams site={site} canEdit={canEdit} isOwner={isOwner} />
      </Row>
      {isOwner && termsCurrent ? <DeleteRow site={site} /> : null}
    </section>
  );
}

// a setting: its name on the left, the control on the right; stacked on a
// phone
function Row({
  label,
  htmlFor,
  children,
}: {
  label: string;
  htmlFor?: string;
  children: ReactNode;
}) {
  return (
    <div className="grid gap-x-6 gap-y-2 border-t border-line-subtle py-4 sm:grid-cols-[240px_minmax(0,560px)] sm:items-start">
      {htmlFor ? (
        <label htmlFor={htmlFor} className="text-body font-medium text-fg sm:pt-2">
          {label}
        </label>
      ) : (
        <span className="text-body font-medium text-fg sm:pt-2">{label}</span>
      )}
      <div className="flex min-w-0 flex-col gap-2">{children}</div>
    </div>
  );
}

function EditSiteForm({ site }: { site: Site }) {
  const initial = { name: site.name, domain: site.domain, origins: site.origins.join("\n") };
  const [values, setValues] = useState<SiteValues>(initial);
  const [errors, setErrors] = useState<SiteErrors>({});
  const [draft, setDraft] = useState("");
  const nameId = useId();
  const originId = useId();
  const save = useMutation({
    mutationFn: (patch: SitePatch) => api.updateSite(site.tenant_id, site.id, patch),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: keys.sites(site.tenant_id) });
      toast.success("Changes saved. New origins apply within a minute.");
    },
    onError: (err) => setErrors(serverSiteErrors(err, values)),
  });
  const origins = parseOrigins(values.origins);
  // an origin typed but not added yet counts as a change, so Save adds it
  const pending = draft.trim() ? [...origins, draft.trim()] : origins;
  const next = { ...values, origins: pending.join("\n") };
  const changed = Object.keys(sitePatch(site, next)).length > 0;

  function setOrigins(list: string[]) {
    setValues((s) => ({ ...s, origins: list.join("\n") }));
    setErrors((e) => ({ ...e, origins: undefined }));
  }

  function addDraft(e: KeyboardEvent<HTMLInputElement>) {
    if (e.key !== "Enter") return;
    e.preventDefault();
    const o = draft.trim();
    if (!o) return;
    if (!origins.includes(o)) setOrigins([...origins, o]);
    setDraft("");
  }

  function submit(e: FormEvent) {
    e.preventDefault();
    const found = validateSite(next);
    setErrors(found);
    if (Object.keys(found).length > 0) return;
    const patch = sitePatch(site, next);
    if (Object.keys(patch).length === 0) return;
    setValues(next);
    setDraft("");
    save.mutate(patch);
  }

  return (
    <form noValidate onSubmit={submit} aria-label={`Edit ${site.name}`} className="flex flex-col">
      <Row label="Name" htmlFor={nameId}>
        <Input
          id={nameId}
          name="name"
          maxLength={100}
          value={values.name}
          aria-invalid={errors.name ? true : undefined}
          aria-describedby={errors.name ? `${nameId}-e` : undefined}
          onChange={(e) => setValues((s) => ({ ...s, name: e.target.value }))}
          className="max-lg:h-(--vs-control-touch)"
        />
        {errors.name ? (
          <p id={`${nameId}-e`} className="text-small text-critical">
            {errors.name}
          </p>
        ) : null}
      </Row>
      <Row label="Allowed origins" htmlFor={originId}>
        <ul aria-label="Allowed origins" className="flex flex-col gap-2">
          {origins.map((o) => (
            <li
              key={o}
              className="flex h-[34px] min-w-0 items-center gap-2 rounded-lg border border-line bg-sunken pr-1 pl-2.5"
            >
              <span className="min-w-0 flex-1 truncate font-mono text-small text-fg" title={o}>
                {o}
              </span>
              <Button
                type="button"
                variant="ghost"
                size="icon-xs"
                aria-label={`Remove ${o}`}
                disabled={origins.length === 1}
                onClick={() => setOrigins(origins.filter((x) => x !== o))}
              >
                <X aria-hidden />
              </Button>
            </li>
          ))}
        </ul>
        <Input
          id={originId}
          name="origin"
          value={draft}
          placeholder="Add an origin"
          autoCapitalize="none"
          spellCheck={false}
          aria-invalid={errors.origins ? true : undefined}
          aria-describedby={errors.origins ? `${originId}-e` : undefined}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={addDraft}
          className="h-[34px] border-dashed font-mono text-small max-lg:h-(--vs-control-touch)"
        />
        {errors.origins ? (
          <p id={`${originId}-e`} className="text-small text-critical">
            {errors.origins}
          </p>
        ) : null}
      </Row>
      <FormMessage
        error={
          save.error && Object.keys(serverSiteErrors(save.error, values)).length === 0
            ? save.error
            : null
        }
      />
      {changed || save.isPending ? (
        <div className="pb-4 sm:grid sm:grid-cols-[240px_minmax(0,560px)] sm:gap-x-6">
          <div className="flex flex-wrap items-center gap-2 sm:col-start-2">
            <Button
              type="submit"
              pending={save.isPending}
              className="max-lg:h-(--vs-control-touch)"
            >
              {save.isPending ? "Saving" : "Save changes"}
            </Button>
            <Button
              type="button"
              variant="ghost"
              className="max-lg:h-(--vs-control-touch)"
              onClick={() => {
                setValues(initial);
                setDraft("");
                setErrors({});
                save.reset();
              }}
            >
              Cancel
            </Button>
          </div>
        </div>
      ) : null}
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
  const disabled = !canEdit || !isOwner || save.isPending;
  const control = (
    <span className="flex items-center gap-3 sm:min-h-9">
      <Switch
        id={id}
        checked={checked}
        aria-labelledby={`${id}-l`}
        disabled={disabled}
        onCheckedChange={(on) => save.mutate(on)}
      />
      <span id={`${id}-l`} className="text-small text-fg-2">
        Keep <code className="font-mono text-caption">utm_*</code> and{" "}
        <code className="font-mono text-caption">ref</code> in page addresses
      </span>
    </span>
  );
  return (
    <div className="flex flex-col gap-2">
      {canEdit && !isOwner ? (
        <Tooltip content="Only an owner can change it.">
          <span className="w-fit">
            {control}
            <span className="sr-only">Only an owner can change it.</span>
          </span>
        </Tooltip>
      ) : (
        control
      )}
      <FormMessage error={save.error} />
    </div>
  );
}

function DeleteRow({ site }: { site: Site }) {
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
    <Row label="Delete site">
      <Button
        variant="destructive-outline"
        className="w-fit max-lg:h-(--vs-control-touch)"
        onClick={() => {
          remove.reset();
          setOpen(true);
        }}
      >
        Delete site
      </Button>
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
    </Row>
  );
}
