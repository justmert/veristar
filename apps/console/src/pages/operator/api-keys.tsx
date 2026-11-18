import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { KeyRound } from "lucide-react";
import { type FormEvent, useState } from "react";
import { ApiError } from "@/api/client";
import { type ApiKeyInput, globalApi } from "@/api/endpoints";
import { apiKeysQuery, apiKeyUsageQuery, keys, keyTenantsQuery } from "@/api/queries";
import type { ApiKey, ApiKeyCreated, ApiKeyStatus } from "@/api/schemas";
import { ChoiceCard } from "@/components/choice-card";
import { CodeBlock } from "@/components/code-block";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { ErrorPanel, FormMessage, Loading, StateBlock } from "@/components/error-panel";
import { SelectField, TextField } from "@/components/form-field";
import { Stagger, StaggerItem } from "@/components/motion";
import { Panel } from "@/components/panel";
import { OneTimeSecret } from "@/components/secret";
import { Pill, type Tone } from "@/components/status";
import { Button } from "@/components/ui/button";
import {
  formatCount,
  formatDate,
  formatDateRange,
  formatDateTimeUtc,
  formatTimeUtc,
} from "@/lib/format";

// contract 9.2 and docs/operators/api-keys.md
export const DEFAULT_EXPIRY_DAYS = 90;
export const MAX_EXPIRY_DAYS = 365;

const STATUS_TONE: Record<ApiKeyStatus, Tone> = {
  active: "success",
  expired: "info",
  revoked: "critical",
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

// the operator layout draws the place header and its tabs; this page starts
// with its own section header
export function ApiKeysPage() {
  const list = useQuery(apiKeysQuery);
  const tenants = useQuery(keyTenantsQuery);
  const [created, setCreated] = useState<ApiKeyCreated | null>(null);
  const names = new Map((tenants.data ?? []).map((t) => [t.id, t.name]));

  return (
    <section
      aria-labelledby="keys-h"
      className="flex max-w-[62.5rem] animate-rise-in flex-col gap-6"
    >
      <header className="flex flex-col gap-1">
        <h2 id="keys-h" className="text-section text-fg">
          API keys
        </h2>
        <p className="max-w-prose text-body text-fg-2">
          Keys give a machine (a warehouse job, a report script, the monitoring prober) read access
          to aggregate metrics under <code className="font-mono text-fg">/api/metrics/v1/</code>. A
          key works nowhere else, and a console session does not work there. Each key makes up to 10
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

      <Panel aria-labelledby="keys-list" title={<span id="keys-list">Keys</span>} flush>
        {list.isPending ? <Loading label="Loading keys" className="px-4 pb-4 sm:px-5" /> : null}
        {list.error ? (
          <div className="px-4 pb-4 sm:px-5">
            <ErrorPanel error={list.error} onRetry={() => list.refetch()} />
          </div>
        ) : null}
        {list.data ? (
          list.data.length === 0 ? (
            <div className="px-4 pb-4 sm:px-5">
              <StateBlock
                kind="empty"
                icon={<KeyRound aria-hidden strokeWidth={1.75} />}
                title="No key has been created yet."
              >
                Create a key above for a machine that reads the metrics API.
              </StateBlock>
            </div>
          ) : (
            <Stagger
              as="ul"
              aria-label="API keys"
              className="divide-y divide-line-subtle border-t border-line"
            >
              {list.data.map((k) => (
                <KeyRow
                  key={k.id}
                  k={k}
                  tenantName={k.tenant_id ? (names.get(k.tenant_id) ?? null) : null}
                />
              ))}
            </Stagger>
          )
        ) : null}
      </Panel>
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
      className="flex flex-col gap-4 rounded-xl border border-line bg-raised p-4 sm:px-5"
    >
      <h3 className="text-title text-fg">Create a key</h3>
      <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_11rem]">
        <TextField
          label="Name"
          hint="Who or what uses the key, for example warehouse."
          placeholder="warehouse"
          maxLength={100}
          className="max-lg:h-(--vs-control-touch)"
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
          className="tabular max-lg:h-(--vs-control-touch)"
          value={form.days}
          error={errors.days}
          onChange={(e) => setForm({ ...form, days: e.target.value })}
        />
      </div>
      <fieldset className="flex flex-col gap-2">
        <legend className="pb-2 text-small font-medium text-fg">Scope: read metrics of</legend>
        <div className="grid gap-2 sm:grid-cols-2">
          <ChoiceCard
            name="scope"
            value="all"
            checked={form.scope === "all"}
            onChange={() => setForm({ ...form, scope: "all" })}
            title="All projects"
            description="The aggregate metrics of every project."
          />
          <ChoiceCard
            name="scope"
            value="tenant"
            checked={form.scope === "tenant"}
            onChange={() => setForm({ ...form, scope: "tenant" })}
            title="One project"
            description="Only the project chosen below."
          />
        </div>
      </fieldset>
      {form.scope === "tenant" ? (
        <div className="flex max-w-md animate-rise-in flex-col gap-2">
          <SelectField
            label="Project"
            value={form.tenantId}
            error={errors.tenantId}
            className="w-full max-lg:h-(--vs-control-touch)"
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
      <div>
        <Button
          type="submit"
          variant="outline"
          pending={create.isPending}
          className="max-lg:h-(--vs-control-touch)"
        >
          {create.isPending ? null : <KeyRound aria-hidden />}
          {create.isPending ? "Creating" : "Create key"}
        </Button>
      </div>
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
    <section aria-label="New key">
      <OneTimeSecret
        value={created.key}
        title={`Copy the key now: ${created.name}`}
        description={`It is shown only once; only its hash is stored. It reads ${
          created.tenant_id ? `the project ${tenantName ?? created.tenant_id}` : "every project"
        } and expires ${formatDateTimeUtc(created.expires_at)}.`}
        copyLabel="Copy key"
        onDone={onDone}
      >
        <details className="group text-small">
          <summary className="w-fit cursor-pointer rounded-sm text-fg-2 transition-colors duration-(--vs-duration-press) ease-standard hover:text-fg focus-ring">
            Try it with curl
          </summary>
          <CodeBlock code={curl} label="curl" copyLabel="Copy command" wrap className="mt-2" />
        </details>
      </OneTimeSecret>
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
    <StaggerItem
      as="li"
      className="flex flex-col gap-3 px-4 py-3 transition-colors duration-(--vs-duration-press) ease-standard hover:bg-hover sm:px-5"
      data-key-id={k.id}
      aria-label={`Key ${k.name}`}
    >
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <div className="flex min-w-0 flex-1 flex-col gap-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="min-w-0 truncate text-body font-medium text-fg">{k.name}</span>
            <Pill tone={STATUS_TONE[k.status]}>{STATUS_TEXT[k.status]}</Pill>
            <code className="font-mono text-caption text-fg-3">{k.prefix}...</code>
          </div>
          <p className="text-caption font-normal text-fg-3">
            Reads {k.tenant_id ? `one project: ${tenantName ?? k.tenant_id}` : "every project"}.
            Created {formatDateTimeUtc(k.created_at)}.{" "}
            {k.revoked_at
              ? `Revoked ${formatDateTimeUtc(k.revoked_at)}.`
              : `${k.status === "expired" ? "Expired" : "Expires"} ${formatDateTimeUtc(k.expires_at)}.`}{" "}
            {k.last_used_at ? `Last used ${formatDateTimeUtc(k.last_used_at)}.` : "Never used."}
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button
            variant="outline"
            size="sm"
            aria-expanded={showUsage}
            className={cn("max-lg:h-(--vs-control-touch)", showUsage && "bg-selected")}
            onClick={() => setShowUsage((v) => !v)}
          >
            {showUsage ? "Hide usage" : "Usage"}
          </Button>
          {k.status !== "revoked" ? (
            <Button
              variant="ghost"
              size="sm"
              className="text-critical max-lg:h-(--vs-control-touch)"
              onClick={() => setConfirm(true)}
            >
              Revoke
            </Button>
          ) : null}
        </div>
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
    </StaggerItem>
  );
}

function Usage({ id }: { id: string }) {
  const u = useQuery(apiKeyUsageQuery(id));
  if (u.isPending) return <Loading label="Loading usage" rows={2} />;
  if (u.error) return <ErrorPanel error={u.error} onRetry={() => u.refetch()} />;
  const d = u.data;
  return (
    <section
      className="flex animate-rise-in flex-col overflow-hidden rounded-lg border border-line bg-sunken text-small"
      aria-label="Usage"
    >
      <div className="flex flex-wrap items-baseline gap-x-6 gap-y-1 border-b border-line px-3 py-2">
        <span className="text-fg-3">{formatDateRange(d.from, d.to)} (UTC)</span>
        <span className="text-fg-2">
          <span className="font-mono font-medium text-fg tabular" data-usage="requests">
            {formatCount(d.totals.requests)}
          </span>{" "}
          {d.totals.requests === 1 ? "request" : "requests"}
        </span>
        <span className="text-fg-2">
          <span
            className={cn(
              "font-mono font-medium tabular",
              d.totals.rejected > 0 ? "text-warning" : "text-fg",
            )}
            data-usage="rejected"
          >
            {formatCount(d.totals.rejected)}
          </span>{" "}
          refused (revoked or expired, over the rate limit, or outside the key's scope)
        </span>
      </div>
      {d.hours.length > 0 ? (
        <div className="max-h-60 overflow-auto">
          <table className="w-full border-separate border-spacing-0 text-left text-caption">
            <thead className="sticky top-0 bg-sunken text-fg-3">
              <tr>
                <th scope="col" className="border-b border-line px-3 py-1 font-medium">
                  Hour (UTC)
                </th>
                <th scope="col" className="border-b border-line px-3 py-1 text-right font-medium">
                  Requests
                </th>
                <th scope="col" className="border-b border-line px-3 py-1 text-right font-medium">
                  Refused
                </th>
              </tr>
            </thead>
            <tbody>
              {d.hours.map((h) => (
                <tr key={h.hour} className="hover:bg-hover">
                  <th
                    scope="row"
                    className="border-b border-line-subtle px-3 py-1 font-normal whitespace-nowrap text-fg-2"
                  >
                    {formatDate(h.hour)}, {formatTimeUtc(h.hour)}
                  </th>
                  <td className="border-b border-line-subtle px-3 py-1 text-right font-mono text-fg tabular">
                    {formatCount(h.requests)}
                  </td>
                  <td
                    className={cn(
                      "border-b border-line-subtle px-3 py-1 text-right font-mono tabular",
                      h.rejected > 0 ? "text-warning" : "text-fg-3",
                    )}
                  >
                    {formatCount(h.rejected)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
    </section>
  );
}
