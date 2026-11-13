import { useMutation, useQuery } from "@tanstack/react-query";
import { Code2, Pencil, Plus, ShieldCheck, Trash2 } from "lucide-react";
import { type FormEvent, useId, useState } from "react";
import { Link, useLocation } from "react-router";
import { ApiError } from "@/api/client";
import { api, type SiteInput, type SitePatch } from "@/api/endpoints";
import { keys, sitesQuery } from "@/api/queries";
import type { Site } from "@/api/schemas";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { ErrorPanel, FormMessage, Loading } from "@/components/error-panel";
import { StellarTomlSummary, VerificationPill } from "@/components/evidence";
import { TextAreaField, TextField } from "@/components/form-field";
import { StatusPill } from "@/components/status";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import * as v from "@/lib/validate";
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

export function SitesPage() {
  const { tenant, isOwner, termsCurrent } = useProject();
  // a suspended project's sites cannot be created or changed (contract 5.11);
  // deleting stays possible
  const canChange = termsCurrent && tenant.status !== "suspended";
  const sites = useQuery(sitesQuery(tenant.id));
  const location = useLocation();
  const justEnrolled = (location.state as { enrolled?: boolean } | null)?.enrolled === true;
  const [adding, setAdding] = useState(false);

  if (sites.isPending) return <Loading label="Loading sites" />;
  if (sites.error) return <ErrorPanel error={sites.error} onRetry={() => sites.refetch()} />;
  const live = sites.data.filter((s) => s.status !== "removed");
  const showForm = adding || live.length === 0;

  return (
    <div className="space-y-6">
      {justEnrolled && live.length === 0 ? (
        <p role="status" className="rounded-lg border border-good/40 bg-good/8 p-4 text-sm">
          Project enrolled. Add its website to get the tracking snippet.
        </p>
      ) : null}

      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 className="text-xl font-semibold tracking-tight">Sites</h2>
        {!showForm && canChange ? (
          <Button onClick={() => setAdding(true)}>
            <Plus aria-hidden />
            Add a site
          </Button>
        ) : null}
      </div>

      {showForm && canChange ? (
        <AddSiteForm
          tenantId={tenant.id}
          onDone={() => setAdding(false)}
          cancellable={live.length > 0}
        />
      ) : null}

      {live.length > 0 ? (
        <ul className="divide-y overflow-hidden rounded-xl border bg-card">
          {live.map((s) => (
            <li key={s.id}>
              <SiteRow site={s} canEdit={canChange} canDelete={isOwner && termsCurrent} />
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}

export function AddSiteForm({
  tenantId,
  onDone,
  cancellable,
}: {
  tenantId: string;
  onDone: () => void;
  cancellable: boolean;
}) {
  const [values, setValues] = useState<SiteValues>({ name: "", domain: "", origins: "" });
  const [errors, setErrors] = useState<SiteErrors>({});
  const create = useMutation({
    mutationFn: (body: SiteInput) => api.createSite(tenantId, body),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: keys.sites(tenantId) });
      onDone();
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
    <form
      noValidate
      onSubmit={submit}
      aria-label="Add a site"
      className="space-y-5 rounded-xl border bg-card p-5 sm:p-6"
    >
      <h3 className="font-semibold">Add a site</h3>
      <div className="grid gap-5 sm:grid-cols-2">
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
          value={values.domain}
          onChange={(e) => setValues((s) => ({ ...s, domain: e.target.value.toLowerCase() }))}
          error={errors.domain}
        />
      </div>
      <OriginsField
        value={values.origins}
        onChange={(origins) => setValues((s) => ({ ...s, origins }))}
        error={errors.origins}
        suggestion={values.origins.trim() === "" ? suggestion : null}
      />
      <div className="flex flex-wrap items-center gap-3">
        <Button type="submit" disabled={create.isPending}>
          {create.isPending ? "Adding" : "Add site"}
        </Button>
        {cancellable ? (
          <Button type="button" variant="ghost" onClick={onDone}>
            Cancel
          </Button>
        ) : null}
        <FormMessage
          error={
            create.error && Object.keys(serverSiteErrors(create.error, values)).length === 0
              ? create.error
              : null
          }
        />
      </div>
    </form>
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
    <div className="space-y-2">
      <TextAreaField
        label="Allowed origins"
        name="origins"
        rows={3}
        className="font-mono text-xs"
        autoCapitalize="none"
        spellCheck={false}
        placeholder="https://example.org"
        hint="One per line. Events are accepted only from pages on these origins: the domain or its subdomains over https, or http://localhost for testing."
        value={value}
        onChange={(e) => onChange(e.target.value)}
        error={error}
      />
      {suggestion ? (
        <Button type="button" variant="outline" size="sm" onClick={() => onChange(suggestion)}>
          Use {suggestion}
        </Button>
      ) : null}
    </div>
  );
}

function SiteRow({
  site,
  canEdit,
  canDelete,
}: {
  site: Site;
  canEdit: boolean;
  canDelete: boolean;
}) {
  const [editing, setEditing] = useState(false);
  return (
    <div className="p-4 sm:p-5">
      <div className="flex flex-wrap items-start gap-x-4 gap-y-3">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <p className="truncate font-medium">{site.name}</p>
            <StatusPill status={site.status} />
          </div>
          <p className="text-sm text-muted-foreground">{site.domain}</p>
          {site.in_review ? (
            <p className="mt-1 text-sm text-warn">
              In review: this domain belonged to a removed project, so an operator checks it before
              it receives traffic.
            </p>
          ) : null}
          {site.status_reason ? (
            <p className="mt-1 text-sm text-muted-foreground">
              Reason given by an operator: {site.status_reason}
            </p>
          ) : null}
          <div className="mt-2">
            <VerificationPill verification={site.verification} />
          </div>
          {editing ? null : (
            <ul className="mt-2 flex flex-wrap gap-1.5" aria-label="Allowed origins">
              {site.origins.map((o) => (
                <li key={o} className="rounded-md bg-muted px-2 py-0.5 font-mono text-xs">
                  {o}
                </li>
              ))}
            </ul>
          )}
        </div>
        <div className="flex flex-wrap gap-2">
          <Button asChild variant="outline" size="sm">
            <Link to={`/projects/${site.tenant_id}/sites/${site.id}/snippet`}>
              <Code2 aria-hidden />
              Snippet
            </Link>
          </Button>
          <Button asChild variant="outline" size="sm">
            <Link to={`/projects/${site.tenant_id}/sites/${site.id}/verify`}>
              <ShieldCheck aria-hidden />
              Verify domain
            </Link>
          </Button>
          {canEdit && !editing ? (
            <Button variant="ghost" size="sm" onClick={() => setEditing(true)}>
              <Pencil aria-hidden />
              Edit
            </Button>
          ) : null}
          {canDelete ? <DeleteSite site={site} /> : null}
        </div>
      </div>
      {editing ? <EditSiteForm site={site} onDone={() => setEditing(false)} /> : null}
      <CampaignParams site={site} canEdit={canEdit} />
      <div className="mt-4 border-t pt-3">
        <StellarTomlSummary
          toml={site.stellar_toml}
          checkedAt={site.stellar_toml_checked_at}
          domain={site.domain}
        />
      </div>
    </div>
  );
}

function EditSiteForm({ site, onDone }: { site: Site; onDone: () => void }) {
  const [values, setValues] = useState<SiteValues>({
    name: site.name,
    domain: site.domain,
    origins: site.origins.join("\n"),
  });
  const [errors, setErrors] = useState<SiteErrors>({});
  const save = useMutation({
    mutationFn: (patch: SitePatch) => api.updateSite(site.tenant_id, site.id, patch),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: keys.sites(site.tenant_id) });
      onDone();
    },
    onError: (err) => setErrors(serverSiteErrors(err, values)),
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    const found = validateSite(values);
    setErrors(found);
    if (Object.keys(found).length > 0) return;
    const patch = sitePatch(site, values);
    if (Object.keys(patch).length === 0) {
      onDone();
      return;
    }
    save.mutate(patch);
  }

  return (
    <form
      noValidate
      onSubmit={submit}
      aria-label={`Edit ${site.name}`}
      className="mt-4 space-y-4 border-t pt-4"
    >
      <TextField
        label="Name"
        name="name"
        maxLength={100}
        value={values.name}
        onChange={(e) => setValues((s) => ({ ...s, name: e.target.value }))}
        error={errors.name}
      />
      <OriginsField
        value={values.origins}
        onChange={(origins) => setValues((s) => ({ ...s, origins }))}
        error={errors.origins}
        suggestion={null}
      />
      <p className="text-xs text-muted-foreground">
        New origins take effect at the collection endpoint within a minute.
      </p>
      <div className="flex flex-wrap items-center gap-3">
        <Button type="submit" size="sm" disabled={save.isPending}>
          {save.isPending ? "Saving" : "Save changes"}
        </Button>
        <Button type="button" variant="ghost" size="sm" onClick={onDone}>
          Cancel
        </Button>
        <FormMessage
          error={
            save.error && Object.keys(serverSiteErrors(save.error, values)).length === 0
              ? save.error
              : null
          }
        />
      </div>
    </form>
  );
}

// utm collection (contract 3.1 collect_utm): off unless a member turns it on.
// saved at once, because it is one switch and not part of the edit form.
export function CampaignParams({ site, canEdit }: { site: Site; canEdit: boolean }) {
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
    <div className="mt-4 space-y-1 border-t pt-3">
      <div className="flex items-start gap-3">
        <Checkbox
          id={id}
          checked={checked}
          disabled={!canEdit || save.isPending}
          onCheckedChange={(c) => save.mutate(c === true)}
          aria-describedby={`${id}-d`}
          className="mt-0.5"
        />
        <label htmlFor={id} className="text-sm leading-snug">
          Keep campaign parameters (<code className="font-mono text-xs">utm_*</code> and{" "}
          <code className="font-mono text-xs">ref</code>) in page addresses
        </label>
      </div>
      <p id={`${id}-d`} className="text-xs text-muted-foreground">
        Off by default: every query parameter is dropped before storage. Turning this on keeps the
        campaign parameters, which France's CNIL recommends not collecting for analytics run without
        consent. It applies to new events within a minute.
      </p>
      <FormMessage error={save.error} />
    </div>
  );
}

function DeleteSite({ site }: { site: Site }) {
  const [open, setOpen] = useState(false);
  const remove = useMutation({
    mutationFn: () => api.deleteSite(site.tenant_id, site.id),
    onSuccess: async () => {
      setOpen(false);
      await queryClient.invalidateQueries({ queryKey: keys.sites(site.tenant_id) });
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
