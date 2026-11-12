import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { KeyRound } from "lucide-react";
import { type FormEvent, useState } from "react";
import { ApiError } from "@/api/client";
import { type ApiKeyInput, globalApi } from "@/api/endpoints";
import { apiKeysQuery, apiKeyUsageQuery, keys, keyTenantsQuery } from "@/api/queries";
import type { ApiKey, ApiKeyCreated, ApiKeyStatus } from "@/api/schemas";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { CopyButton } from "@/components/copy-button";
import { ErrorPanel, FormMessage, Loading } from "@/components/error-panel";
import { SelectField, TextField } from "@/components/form-field";
import { Button } from "@/components/ui/button";
import { formatCount, formatDateTimeUtc, formatHourUtc } from "@/lib/format";
import { formatDay } from "@/lib/global";

// contract 9.2 and docs/operators/api-keys.md
export const DEFAULT_EXPIRY_DAYS = 90;
export const MAX_EXPIRY_DAYS = 365;

const STATUS_STYLE: Record<ApiKeyStatus, string> = {
  active: "border-good/40 bg-good/10",
  expired: "bg-muted text-muted-foreground",
  revoked: "border-destructive/40 bg-destructive/5",
};

const STATUS_TEXT: Record<ApiKeyStatus, string> = {
  active: "Active",
  expired: "Expired",
  revoked: "Revoked",
};

export type KeyForm = { name: string; scope: "all" | "tenant"; tenantId: string; days: string };

// contract 3.2: control characters other than newline and tab are refused
function isControl(ch: string): boolean {
  const c = ch.codePointAt(0) ?? 0;
  return (c < 0x20 && ch !== "\n" && ch !== "\t") || c === 0x7f;
}

// the field rules of 9.2, checked before sending so the form can say which
// field is wrong; the server checks them again
export function validateKeyForm(f: KeyForm): {
  errors: Partial<Record<keyof KeyForm, string>>;
  body: ApiKeyInput | null;
} {
  const errors: Partial<Record<keyof KeyForm, string>> = {};
  const name = f.name.trim();
  if (!name) errors.name = "Enter a name.";
  else if ([...name].length > 100) errors.name = "Use at most 100 characters.";
  else if ([...name].some(isControl)) errors.name = "Remove control characters.";
  if (f.scope === "tenant" && !f.tenantId) errors.tenantId = "Choose a project.";
  const days = Number(f.days);
  if (!/^\d+$/.test(f.days) || days < 1 || days > MAX_EXPIRY_DAYS)
    errors.days = `Enter a whole number of days from 1 to ${MAX_EXPIRY_DAYS}.`;
  if (Object.keys(errors).length) return { errors, body: null };
  const body: ApiKeyInput = { name, scope: "metrics:read", expires_in_days: days };
  if (f.scope === "tenant") body.tenant_id = f.tenantId;
  return { errors, body };
}

// 422 field codes of 9.2 mapped onto the form's fields
function serverFieldErrors(err: unknown): Partial<Record<keyof KeyForm, string>> {
  if (!(err instanceof ApiError) || err.code !== "validation_failed") return {};
  const out: Partial<Record<keyof KeyForm, string>> = {};
  for (const [field, code] of Object.entries(err.fields)) {
    if (field === "name")
      out.name = code === "too_long" ? "Use at most 100 characters." : "Enter a valid name.";
    if (field === "tenant_id")
      out.tenantId =
        code === "unknown_tenant"
          ? "That project does not exist or was removed."
          : "Choose a project.";
    if (field === "expires_in_days") out.days = `Enter 1 to ${MAX_EXPIRY_DAYS} days.`;
  }
  return out;
}

export function ApiKeysPage() {
  const list = useQuery(apiKeysQuery);
  const tenants = useQuery(keyTenantsQuery);
  const [created, setCreated] = useState<ApiKeyCreated | null>(null);
  const names = new Map((tenants.data ?? []).map((t) => [t.id, t.name]));

  return (
    <section className="space-y-6">
      <header>
        <h2 className="text-xl font-semibold tracking-tight">API keys</h2>
        <p className="mt-1 max-w-prose text-sm text-muted-foreground">
          Keys give a machine (a warehouse job, a report script, the monitoring prober) read access
          to aggregate metrics under <code className="font-mono">/api/metrics/v1/</code>. A key
          works nowhere else, and a console session does not work there. Each key may make 10
          requests per second.
        </p>
      </header>

      {created ? (
        <CreatedKey
          created={created}
          tenantName={names.get(created.tenant_id ?? "") ?? null}
          onDone={() => setCreated(null)}
        />
      ) : (
        <CreateKeyForm
          tenants={tenants.data?.map((t) => ({ id: t.id, name: t.name })) ?? []}
          tenantsError={tenants.error}
          onCreated={setCreated}
        />
      )}

      <section aria-labelledby="keys-list" className="space-y-3">
        <h3 id="keys-list" className="font-semibold">
          Keys
        </h3>
        {list.isPending ? <Loading label="Loading keys" /> : null}
        {list.error ? <ErrorPanel error={list.error} onRetry={() => list.refetch()} /> : null}
        {list.data ? (
          list.data.length === 0 ? (
            <p className="rounded-xl border border-dashed bg-card p-6 text-muted-foreground">
              No key has been created yet.
            </p>
          ) : (
            <ul
              aria-label="API keys"
              className="divide-y overflow-hidden rounded-xl border bg-card"
            >
              {list.data.map((k) => (
                <KeyRow
                  key={k.id}
                  k={k}
                  tenantName={k.tenant_id ? (names.get(k.tenant_id) ?? null) : null}
                />
              ))}
            </ul>
          )
        ) : null}
      </section>
    </section>
  );
}

function CreateKeyForm({
  tenants,
  tenantsError,
  onCreated,
}: {
  tenants: { id: string; name: string }[];
  tenantsError: unknown;
  onCreated: (k: ApiKeyCreated) => void;
}) {
  const qc = useQueryClient();
  const [form, setForm] = useState<KeyForm>({
    name: "",
    scope: "all",
    tenantId: "",
    days: String(DEFAULT_EXPIRY_DAYS),
  });
  const [errors, setErrors] = useState<Partial<Record<keyof KeyForm, string>>>({});
  const create = useMutation({
    mutationFn: (body: ApiKeyInput) => globalApi.createApiKey(body),
    onSuccess: (k) => {
      qc.invalidateQueries({ queryKey: keys.opKeys });
      onCreated(k);
    },
    onError: (err) => setErrors(serverFieldErrors(err)),
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    const v = validateKeyForm(form);
    setErrors(v.errors);
    if (v.body) create.mutate(v.body);
  }

  return (
    <form
      aria-label="Create an API key"
      onSubmit={submit}
      noValidate
      className="space-y-4 rounded-xl border bg-card p-4 sm:p-5"
    >
      <h3 className="font-semibold">Create a key</h3>
      <div className="grid gap-4 sm:grid-cols-2">
        <TextField
          label="Name"
          hint="Who or what uses the key, for example warehouse."
          maxLength={100}
          value={form.name}
          error={errors.name}
          onChange={(e) => setForm({ ...form, name: e.target.value })}
        />
        <TextField
          label="Expires after (days)"
          hint={`${DEFAULT_EXPIRY_DAYS} by default, at most ${MAX_EXPIRY_DAYS}.`}
          type="number"
          inputMode="numeric"
          min={1}
          max={MAX_EXPIRY_DAYS}
          value={form.days}
          error={errors.days}
          onChange={(e) => setForm({ ...form, days: e.target.value })}
        />
      </div>
      <fieldset className="space-y-2 text-sm">
        <legend className="mb-1 font-medium">Scope: read metrics of</legend>
        <label className="flex items-center gap-2">
          <input
            type="radio"
            name="scope"
            className="size-4 accent-primary"
            checked={form.scope === "all"}
            onChange={() => setForm({ ...form, scope: "all" })}
          />
          All projects
        </label>
        <label className="flex items-center gap-2">
          <input
            type="radio"
            name="scope"
            className="size-4 accent-primary"
            checked={form.scope === "tenant"}
            onChange={() => setForm({ ...form, scope: "tenant" })}
          />
          One project
        </label>
      </fieldset>
      {form.scope === "tenant" ? (
        <div className="max-w-md">
          <SelectField
            label="Project"
            value={form.tenantId}
            error={errors.tenantId}
            onChange={(e) => setForm({ ...form, tenantId: e.target.value })}
          >
            <option value="">Choose a project</option>
            {tenants.map((t) => (
              <option key={t.id} value={t.id}>
                {t.name}
              </option>
            ))}
          </SelectField>
          {tenantsError ? <FormMessage error={tenantsError} /> : null}
        </div>
      ) : null}
      {create.error && Object.keys(serverFieldErrors(create.error)).length === 0 ? (
        <FormMessage error={create.error} />
      ) : null}
      <Button type="submit" disabled={create.isPending}>
        <KeyRound aria-hidden />
        {create.isPending ? "Creating" : "Create key"}
      </Button>
    </form>
  );
}

function CreatedKey({
  created,
  tenantName,
  onDone,
}: {
  created: ApiKeyCreated;
  tenantName: string | null;
  onDone: () => void;
}) {
  const origin = window.location.origin;
  const curl = `curl -H "Authorization: Bearer ${created.key}" \\\n  "${origin}/api/metrics/v1/tenants"`;
  return (
    <section
      aria-label="New key"
      className="space-y-3 rounded-xl border border-primary/40 bg-card p-4 sm:p-5"
    >
      <h3 className="font-semibold">Key {created.name} created</h3>
      <p className="text-sm text-muted-foreground">
        Copy the key now. It is shown only once; only its hash is stored. It reads{" "}
        {created.tenant_id ? `the project ${tenantName ?? created.tenant_id}` : "every project"} and
        expires {formatDateTimeUtc(created.expires_at)}.
      </p>
      <div className="flex flex-wrap items-center gap-2">
        <code
          data-testid="new-api-key"
          className="min-w-0 rounded-lg border bg-muted px-3 py-2 font-mono text-sm break-all"
        >
          {created.key}
        </code>
        <CopyButton text={created.key} label="Copy key" />
      </div>
      <details className="text-sm">
        <summary className="cursor-pointer text-muted-foreground hover:text-foreground">
          Try it with curl
        </summary>
        <pre className="mt-2 overflow-x-auto rounded-lg border bg-muted p-3 font-mono text-xs">
          {curl}
        </pre>
      </details>
      <Button variant="outline" onClick={onDone}>
        I have copied the key
      </Button>
    </section>
  );
}

function KeyRow({ k, tenantName }: { k: ApiKey; tenantName: string | null }) {
  const qc = useQueryClient();
  const [confirm, setConfirm] = useState(false);
  const [showUsage, setShowUsage] = useState(false);
  const revoke = useMutation({
    mutationFn: () => globalApi.revokeApiKey(k.id),
    onSuccess: () => {
      setConfirm(false);
      qc.invalidateQueries({ queryKey: keys.opKeys });
    },
  });
  return (
    <li className="space-y-2 px-4 py-4 sm:px-5" data-key-id={k.id} aria-label={`Key ${k.name}`}>
      <div className="flex flex-wrap items-center gap-2">
        <p className="font-medium">{k.name}</p>
        <span
          className={`rounded-full border px-2 py-0.5 text-xs font-medium ${STATUS_STYLE[k.status]}`}
        >
          {STATUS_TEXT[k.status]}
        </span>
        <code className="font-mono text-xs text-muted-foreground">{k.prefix}...</code>
      </div>
      <p className="text-xs text-muted-foreground">
        Reads {k.tenant_id ? `one project: ${tenantName ?? k.tenant_id}` : "every project"}. Created{" "}
        {formatDateTimeUtc(k.created_at)}.{" "}
        {k.revoked_at
          ? `Revoked ${formatDateTimeUtc(k.revoked_at)}.`
          : `${k.status === "expired" ? "Expired" : "Expires"} ${formatDateTimeUtc(k.expires_at)}.`}{" "}
        {k.last_used_at ? `Last used ${formatDateTimeUtc(k.last_used_at)}.` : "Never used."}
      </p>
      <div className="flex flex-wrap gap-2">
        <Button
          variant="outline"
          size="sm"
          aria-expanded={showUsage}
          onClick={() => setShowUsage((v) => !v)}
        >
          {showUsage ? "Hide usage" : "Usage"}
        </Button>
        {k.status !== "revoked" ? (
          <Button
            variant="outline"
            size="sm"
            className="text-destructive"
            onClick={() => setConfirm(true)}
          >
            Revoke
          </Button>
        ) : null}
      </div>
      {showUsage ? <Usage id={k.id} /> : null}
      <ConfirmDialog
        open={confirm}
        onOpenChange={(o) => {
          setConfirm(o);
          if (!o) revoke.reset();
        }}
        title={`Revoke the key ${k.name}?`}
        description="The next request with this key is refused. A revoked key cannot be restored; create a new one instead."
        confirmLabel="Revoke key"
        pendingLabel="Revoking"
        pending={revoke.isPending}
        error={revoke.error}
        onConfirm={() => revoke.mutate()}
      />
    </li>
  );
}

function Usage({ id }: { id: string }) {
  const u = useQuery(apiKeyUsageQuery(id));
  if (u.isPending) return <Loading label="Loading usage" />;
  if (u.error) return <ErrorPanel error={u.error} onRetry={() => u.refetch()} />;
  const d = u.data;
  return (
    <section className="space-y-2 rounded-lg border p-3 text-sm" aria-label="Usage">
      <p>
        {formatDay(d.from)} to {formatDay(d.to)} (UTC):{" "}
        <span className="tabular font-medium" data-usage="requests">
          {formatCount(d.totals.requests)}
        </span>{" "}
        {d.totals.requests === 1 ? "request" : "requests"},{" "}
        <span className="tabular font-medium" data-usage="rejected">
          {formatCount(d.totals.rejected)}
        </span>{" "}
        refused (revoked or expired, over the rate limit, or outside the key's scope).
      </p>
      {d.hours.length > 0 ? (
        <div className="max-h-60 overflow-auto rounded-lg border">
          <table className="w-full text-left text-sm">
            <thead className="sticky top-0 bg-muted text-xs text-muted-foreground">
              <tr>
                <th scope="col" className="px-3 py-1.5 font-medium">
                  Hour
                </th>
                <th scope="col" className="px-3 py-1.5 text-right font-medium">
                  Requests
                </th>
                <th scope="col" className="px-3 py-1.5 text-right font-medium">
                  Refused
                </th>
              </tr>
            </thead>
            <tbody className="divide-y">
              {d.hours.map((h) => (
                <tr key={h.hour}>
                  <th scope="row" className="px-3 py-1 font-normal whitespace-nowrap">
                    {formatDay(h.hour.slice(0, 10))}, {formatHourUtc(h.hour)}
                  </th>
                  <td className="tabular px-3 py-1 text-right">{formatCount(h.requests)}</td>
                  <td className="tabular px-3 py-1 text-right">{formatCount(h.rejected)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
    </section>
  );
}
