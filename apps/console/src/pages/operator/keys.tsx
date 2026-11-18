import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Ban, KeyRound, MoreHorizontal } from "lucide-react";
import { type FormEvent, useState } from "react";
import { globalApi } from "@/api/endpoints";
import {
  clientSnippets,
  type McpCredential,
  type McpCredentialCreated,
  mcpApi,
  mcpEndpoint,
  mcpKeys,
  operatorMcpQuery,
  REVOKED_REASON,
} from "@/api/mcp";
import { apiKeysQuery, apiKeyUsageQuery, keys, keyTenantsQuery } from "@/api/queries";
import type { ApiKey, ApiKeyCreated } from "@/api/schemas";
import { ChoiceCard } from "@/components/choice-card";
import { CodeBlock, CopyField } from "@/components/code-block";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { type Column, DataTable } from "@/components/data-table";
import { FormMessage } from "@/components/error-panel";
import { SelectField, TextField } from "@/components/form-field";
import { PageHeader } from "@/components/page-header";
import { SecretPanel } from "@/components/secret-panel";
import { ErrorState, LoadingState, Missing, Skeleton } from "@/components/states";
import { StatusBadge } from "@/components/status-badge";
import { toast } from "@/components/toast";
import { Button } from "@/components/ui/button";
import { Menu, MenuContent, MenuItem, MenuTrigger } from "@/components/ui/menu";
import {
  DEFAULT_EXPIRY_DAYS,
  EMPTY_KEY_FORM,
  type KeyErrors,
  type KeyForm,
  MAX_EXPIRY_DAYS,
  serverKeyErrors,
  validateKeyForm,
} from "@/lib/access-keys";
import {
  formatCount,
  formatDate,
  formatDateRange,
  formatDateTimeUtc,
  formatTimeUtc,
} from "@/lib/format";

// one row of the list: a machine api key, an ai assistant key, or the
// connection of an ai client that signed in through this console
type KeyRow = {
  id: string;
  kind: "machine" | "assistant" | "connector";
  name: string;
  prefix: string | null;
  status: "active" | "expired" | "revoked";
  reads: string;
  created: string;
  expires: string | null;
  revoked: string | null;
  revokedReason: string | null;
  lastUsed: string | null;
};

const KIND_TEXT: Record<KeyRow["kind"], string> = {
  machine: "Machine API key",
  assistant: "AI assistant key",
  connector: "AI connection",
};

function machineRow(k: ApiKey, names: Map<string, string>): KeyRow {
  return {
    id: k.id,
    kind: "machine",
    name: k.name,
    prefix: k.prefix,
    status: k.status,
    reads: k.tenant_id
      ? `One project: ${names.get(k.tenant_id) ?? "a removed project"}`
      : "Every project",
    created: k.created_at,
    expires: k.expires_at,
    revoked: k.revoked_at,
    revokedReason: null,
    lastUsed: k.last_used_at,
  };
}

function assistantRow(c: McpCredential): KeyRow {
  return {
    id: c.id,
    kind: c.kind === "key" ? "assistant" : "connector",
    name: c.name,
    prefix: c.prefix,
    status: c.status,
    reads: `Operator view, as ${c.display_name ?? "its creator"}`,
    created: c.created_at,
    expires: c.expires_at,
    revoked: c.revoked_at,
    revokedReason: c.revoked_reason ? (REVOKED_REASON[c.revoked_reason] ?? null) : null,
    lastUsed: c.last_seen_at,
  };
}

const STATUS_ORDER = { active: 0, expired: 1, revoked: 2 } as const;

// read-only keys for machines and for ai assistants, on one page: one form
// creates either, one list shows and revokes both
export function AccessKeysPage() {
  const qc = useQueryClient();
  const machines = useQuery(apiKeysQuery);
  const assistants = useQuery(operatorMcpQuery);
  const tenants = useQuery(keyTenantsQuery);
  const [created, setCreated] = useState<
    | { type: "machine"; key: ApiKeyCreated }
    | { type: "assistant"; key: McpCredentialCreated }
    | null
  >(null);
  const [revoking, setRevoking] = useState<KeyRow | null>(null);
  const [usage, setUsage] = useState<KeyRow | null>(null);
  const names = new Map((tenants.data ?? []).map((t) => [t.id, t.name]));

  const rows: KeyRow[] = [
    ...(machines.data ?? []).map((k) => machineRow(k, names)),
    ...(assistants.data ?? []).map(assistantRow),
  ].sort(
    (a, b) => STATUS_ORDER[a.status] - STATUS_ORDER[b.status] || b.created.localeCompare(a.created),
  );
  const error = machines.error ?? assistants.error;

  const revoke = useMutation({
    mutationFn: (r: KeyRow) =>
      r.kind === "machine" ? globalApi.revokeApiKey(r.id) : mcpApi.revokeOperatorCredential(r.id),
    onSuccess: async (_d, r) => {
      setRevoking(null);
      toast.success(`${r.name} is revoked. Its next request is refused.`);
      await Promise.all([
        qc.invalidateQueries({ queryKey: keys.opKeys }),
        qc.invalidateQueries({ queryKey: mcpKeys.operator }),
      ]);
    },
  });

  const columns: Column<KeyRow>[] = [
    {
      key: "name",
      header: "Name",
      primary: true,
      minWidth: "min-w-48",
      cell: (r) => (
        <span className="flex min-w-0 flex-col">
          <span className="truncate font-medium text-fg">{r.name}</span>
          {r.prefix ? <span className="font-mono text-caption text-fg-2">{r.prefix}…</span> : null}
        </span>
      ),
    },
    { key: "kind", header: "Type", mobile: true, cell: (r) => KIND_TEXT[r.kind] },
    {
      key: "status",
      header: "Status",
      mobile: true,
      cell: (r) => <StatusBadge kind="key" status={r.status} />,
    },
    {
      key: "reads",
      header: "Reads",
      minWidth: "min-w-40",
      className: "max-w-64",
      truncate: (r) => r.reads,
      cell: (r) => r.reads,
    },
    {
      key: "used",
      header: "Last used",
      cell: (r) =>
        r.lastUsed ? (
          <span className="whitespace-nowrap">{formatDateTimeUtc(r.lastUsed)}</span>
        ) : (
          <Missing reason="Never used" />
        ),
    },
    {
      key: "until",
      header: "Ends",
      cell: (r) =>
        r.revoked ? (
          <span className="whitespace-nowrap">
            Revoked {formatDate(r.revoked)}
            {r.revokedReason ? `, ${r.revokedReason}` : ""}
          </span>
        ) : r.expires ? (
          <span className="whitespace-nowrap">
            {r.status === "expired" ? "Expired" : "Expires"} {formatDate(r.expires)}
          </span>
        ) : (
          <span className="whitespace-nowrap">While the client renews it</span>
        ),
    },
    {
      key: "actions",
      mobile: true,
      header: "Actions",
      align: "right",
      cell: (r) =>
        r.kind !== "machine" && r.status === "revoked" ? null : (
          <Menu>
            <MenuTrigger asChild>
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label={`Actions for ${r.name}`}
                className="max-lg:size-(--vs-control-touch)"
              >
                <MoreHorizontal aria-hidden />
              </Button>
            </MenuTrigger>
            <MenuContent align="end">
              {r.kind === "machine" ? (
                <MenuItem onSelect={() => setUsage(r)}>Show usage</MenuItem>
              ) : null}
              {r.status !== "revoked" ? (
                <MenuItem
                  tone="critical"
                  onSelect={() => {
                    revoke.reset();
                    setRevoking(r);
                  }}
                >
                  <Ban aria-hidden />
                  Revoke
                </MenuItem>
              ) : null}
            </MenuContent>
          </Menu>
        ),
    },
  ];

  return (
    <div className="flex min-w-0 flex-col gap-8">
      <PageHeader
        title="Access keys"
        description="Read-only keys for machines and AI assistants. A key reads daily totals and changes nothing."
      />

      {created ? (
        <CreatedKey
          created={created}
          tenantName={
            created.type === "machine" && created.key.tenant_id
              ? (names.get(created.key.tenant_id) ?? null)
              : null
          }
          onDone={() => setCreated(null)}
        />
      ) : (
        <CreateKeyForm
          tenants={tenants.data?.map((t) => ({ id: t.id, name: t.name })) ?? []}
          tenantsError={tenants.error}
          onCreated={(c) => {
            toast.success(`${c.key.name} is created. Copy it now.`);
            setCreated(c);
          }}
        />
      )}

      <section aria-labelledby="keys-list-h" className="flex min-w-0 flex-col gap-3">
        <h2 id="keys-list-h" className="text-title text-fg">
          Keys and connections
        </h2>
        {machines.isPending || assistants.isPending ? (
          <LoadingState label="Loading the keys">
            <Skeleton.Table rows={3} columns={5} />
          </LoadingState>
        ) : error ? (
          <ErrorState
            title="The keys did not load"
            error={error}
            onRetry={() => {
              machines.refetch();
              assistants.refetch();
            }}
          />
        ) : (
          <DataTable
            label="Keys and connections"
            columns={columns}
            rows={rows}
            getRowKey={(r) => r.id}
            rowLabel={(r) => r.name}
            rowProps={(r) => ({ "data-key-id": r.id, "data-kind": r.kind })}
            empty={{
              title: "No key yet",
              text: "Create a key above for a machine or an AI assistant.",
            }}
          />
        )}
        {usage ? <Usage row={usage} onClose={() => setUsage(null)} /> : null}
      </section>

      <ConnectSteps />

      <ConfirmDialog
        open={revoking !== null}
        onOpenChange={(o) => {
          if (!o) {
            setRevoking(null);
            revoke.reset();
          }
        }}
        title={`Revoke ${revoking?.name ?? "the key"}?`}
        description="The next request with it is refused. You cannot restore it. Create a new key instead."
        confirmLabel="Revoke"
        pendingLabel="Revoking"
        pending={revoke.isPending}
        error={revoke.error}
        onConfirm={() => revoking && revoke.mutate(revoking)}
      />
    </div>
  );
}

function CreateKeyForm({
  tenants,
  tenantsError,
  onCreated,
}: {
  tenants: { id: string; name: string }[];
  tenantsError: unknown;
  onCreated: (
    c: { type: "machine"; key: ApiKeyCreated } | { type: "assistant"; key: McpCredentialCreated },
  ) => void;
}) {
  const qc = useQueryClient();
  const [form, setForm] = useState<KeyForm>(EMPTY_KEY_FORM);
  const [errors, setErrors] = useState<KeyErrors>({});
  const create = useMutation({
    mutationFn: async (
      b: NonNullable<ReturnType<typeof validateKeyForm>["body"]>,
    ): Promise<
      { type: "machine"; key: ApiKeyCreated } | { type: "assistant"; key: McpCredentialCreated }
    > =>
      b.type === "machine"
        ? { type: "machine", key: await globalApi.createApiKey(b.body) }
        : { type: "assistant", key: await mcpApi.createOperatorKey(b.body) },
    onSuccess: async (c) => {
      await qc.invalidateQueries({
        queryKey: c.type === "machine" ? keys.opKeys : mcpKeys.operator,
      });
      setForm(EMPTY_KEY_FORM);
      onCreated(c);
    },
    onError: (err) => setErrors(serverKeyErrors(err)),
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    const v = validateKeyForm(form);
    setErrors(v.errors);
    if (v.body) create.mutate(v.body);
  }

  const machine = form.type === "machine";
  return (
    <section aria-labelledby="create-key-h" className="flex max-w-3xl flex-col gap-4">
      <h2 id="create-key-h" className="text-title text-fg">
        Create a key
      </h2>
      <form
        aria-label="Create a key"
        onSubmit={submit}
        noValidate
        className="flex flex-col gap-4 rounded-lg border border-line bg-raised p-4 sm:p-5"
      >
        <fieldset className="flex flex-col gap-2">
          <legend className="pb-2 text-small font-medium text-fg">Type</legend>
          <div className="grid gap-2 sm:grid-cols-2">
            <ChoiceCard
              name="key-type"
              value="machine"
              checked={machine}
              onChange={() => setForm({ ...form, type: "machine" })}
              title="Machine API key"
              description="For a script or a job that reads the metrics API. Up to 10 requests a second."
            />
            <ChoiceCard
              name="key-type"
              value="assistant"
              checked={!machine}
              onChange={() => setForm({ ...form, type: "assistant" })}
              title="AI assistant key"
              description="For an AI client that searches, ranks and compares projects. It acts as you."
            />
          </div>
        </fieldset>
        <div className="grid gap-4 sm:grid-cols-[minmax(0,1fr)_11rem]">
          <TextField
            label="Name"
            hint={
              machine
                ? "Who uses the key, for example warehouse."
                : "Where you use the key, for example laptop."
            }
            placeholder={machine ? "warehouse" : "laptop"}
            maxLength={100}
            className="max-lg:h-(--vs-control-touch)"
            value={form.name}
            error={errors.name}
            onChange={(e) => setForm({ ...form, name: e.target.value })}
          />
          <TextField
            label="Expires after, days"
            hint={`${DEFAULT_EXPIRY_DAYS} by default, at most ${MAX_EXPIRY_DAYS}.`}
            inputMode="numeric"
            className="tabular max-lg:h-(--vs-control-touch)"
            value={form.days}
            error={errors.days}
            onChange={(e) => setForm({ ...form, days: e.target.value })}
          />
        </div>
        {machine ? (
          <fieldset className="flex flex-col gap-2">
            <legend className="pb-2 text-small font-medium text-fg">Reads the metrics of</legend>
            <div className="grid gap-2 sm:grid-cols-2">
              <ChoiceCard
                name="scope"
                value="all"
                checked={form.scope === "all"}
                onChange={() => setForm({ ...form, scope: "all" })}
                title="Every project"
                description="The daily totals of every project."
              />
              <ChoiceCard
                name="scope"
                value="tenant"
                checked={form.scope === "tenant"}
                onChange={() => setForm({ ...form, scope: "tenant" })}
                title="One project"
                description="Only the project you choose."
              />
            </div>
            {form.scope === "tenant" ? (
              <div className="flex max-w-md animate-rise-in flex-col gap-2 pt-2">
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
          </fieldset>
        ) : null}
        {create.error && Object.keys(serverKeyErrors(create.error)).length === 0 ? (
          <FormMessage error={create.error} />
        ) : null}
        <div>
          <Button
            type="submit"
            pending={create.isPending}
            className="max-lg:h-(--vs-control-touch)"
          >
            {create.isPending ? null : <KeyRound aria-hidden />}
            {create.isPending ? "Creating" : "Create key"}
          </Button>
        </div>
      </form>
    </section>
  );
}

function CreatedKey({
  created,
  tenantName,
  onDone,
}: {
  created:
    | { type: "machine"; key: ApiKeyCreated }
    | { type: "assistant"; key: McpCredentialCreated };
  tenantName: string | null;
  onDone: () => void;
}) {
  const origin = window.location.origin;
  if (created.type === "machine") {
    const k = created.key;
    const curl = `curl -H "Authorization: Bearer ${k.key}" \\\n  "${origin}/api/metrics/v1/tenants"`;
    return (
      <SecretPanel
        value={k.key}
        title={`Copy the key now: ${k.name}`}
        description={`The service keeps only a hash, so it shows the key once. It reads ${
          k.tenant_id ? `the project ${tenantName ?? "you chose"}` : "every project"
        }. It expires ${formatDateTimeUtc(k.expires_at)}.`}
        copyLabel="Copy key"
        onDone={onDone}
      >
        <CodeBlock code={curl} label="Try it with curl" copyLabel="Copy command" wrap />
      </SecretPanel>
    );
  }
  const k = created.key;
  const s = clientSnippets(origin, k.key);
  return (
    <SecretPanel
      value={k.key}
      title={`Copy the key now: ${k.name}`}
      description={`The service keeps only a hash, so it shows the key once. It expires ${
        k.expires_at ? formatDateTimeUtc(k.expires_at) : "never"
      }.`}
      copyLabel="Copy key"
      onDone={onDone}
    >
      <CodeBlock
        code={s.claudeCode}
        label="Claude Code, with this key"
        copyLabel="Copy command"
        wrap
      />
      <CodeBlock
        code={s.cursor}
        label="Cursor, in .cursor/mcp.json"
        copyLabel="Copy configuration"
      />
    </SecretPanel>
  );
}

// how to connect an ai client: the server address once, then the steps of
// each client in order
function ConnectSteps() {
  const origin = window.location.origin;
  const s = clientSnippets(origin, null);
  return (
    <section aria-labelledby="connect-h" className="flex max-w-3xl flex-col gap-4">
      <div className="flex flex-col gap-1">
        <h2 id="connect-h" className="text-title text-fg">
          Connect an AI assistant
        </h2>
        <p className="text-small text-fg-2">
          The assistant gets the operator view: it searches, ranks and compares projects. It changes
          nothing. It stops when you lose operator access.
        </p>
      </div>
      <div className="flex min-w-0 flex-col gap-2">
        <span className="text-caption text-fg-2">Server address</span>
        <CopyField
          value={mcpEndpoint(origin)}
          label="server address"
          className="min-w-0 max-w-full"
        />
      </div>
      <ClientSteps title="Claude and ChatGPT, no key">
        <li>
          In claude.ai or Claude Desktop, open Settings, then Connectors, then Add custom connector.
          In ChatGPT, open Settings, then Connectors, then Create.
        </li>
        <li>Paste the server address. Leave the client credentials empty.</li>
        <li>Sign in to this console when the client opens it.</li>
        <li>Choose the operator view and approve.</li>
      </ClientSteps>
      <ClientSteps title="Claude Code">
        <li>Create an AI assistant key above.</li>
        <li>
          Run this command with the key:
          <CodeBlock code={s.claudeCode} copyLabel="Copy command" wrap className="mt-2" />
        </li>
        <li>
          Or sign in through the browser instead of a key:
          <CodeBlock code={s.claudeCodeOAuth} copyLabel="Copy command" wrap className="mt-2" />
        </li>
      </ClientSteps>
      <ClientSteps title="Cursor">
        <li>Create an AI assistant key above.</li>
        <li>
          Add this to .cursor/mcp.json, or to ~/.cursor/mcp.json for every project:
          <CodeBlock code={s.cursor} copyLabel="Copy configuration" className="mt-2" />
        </li>
        <li>Put the key in place of the text in angle brackets.</li>
      </ClientSteps>
    </section>
  );
}

function ClientSteps({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <details className="group rounded-lg border border-line bg-raised">
      <summary className="cursor-pointer rounded-lg px-4 py-3 text-body font-medium text-fg transition-colors duration-(--vs-duration-press) ease-standard hover:bg-hover focus-ring max-lg:min-h-(--vs-control-touch)">
        {title}
      </summary>
      <ol className="flex list-decimal flex-col gap-3 border-t border-line py-4 pr-4 pl-10 text-small text-fg">
        {children}
      </ol>
    </details>
  );
}

// a machine key's requests per utc hour over the last 7 days
function Usage({ row, onClose }: { row: KeyRow; onClose: () => void }) {
  const u = useQuery(apiKeyUsageQuery(row.id));
  return (
    <section
      aria-label={`Usage of ${row.name}`}
      className="flex min-w-0 animate-rise-in flex-col gap-3 rounded-lg border border-line bg-raised p-4 sm:p-5"
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="text-title break-words text-fg">Usage of {row.name}</h3>
        <Button
          variant="ghost"
          size="sm"
          onClick={onClose}
          className="max-lg:h-(--vs-control-touch)"
        >
          Close
        </Button>
      </div>
      {u.isPending ? (
        <LoadingState label="Loading the usage">
          <Skeleton.Table rows={3} columns={3} />
        </LoadingState>
      ) : u.error ? (
        <ErrorState
          compact
          title="The usage did not load"
          error={u.error}
          onRetry={() => u.refetch()}
        />
      ) : (
        <>
          <p className="text-small text-fg-2">
            {formatDateRange(u.data.from, u.data.to)}, UTC:{" "}
            <span className="font-mono text-fg tabular" data-usage="requests">
              {formatCount(u.data.totals.requests)}
            </span>{" "}
            requests,{" "}
            <span className="font-mono text-fg tabular" data-usage="rejected">
              {formatCount(u.data.totals.rejected)}
            </span>{" "}
            refused. A request is refused when the key is revoked or expired, over the rate limit,
            or outside its scope.
          </p>
          <DataTable
            label={`Requests per hour of ${row.name}`}
            columns={[
              {
                key: "hour",
                header: "Hour, UTC",
                primary: true,
                cell: (h) => `${formatDate(h.hour)}, ${formatTimeUtc(h.hour)}`,
              },
              {
                key: "requests",
                header: "Requests",
                align: "right",
                numeric: true,
                mobile: true,
                cell: (h) => formatCount(h.requests),
              },
              {
                key: "rejected",
                header: "Refused",
                align: "right",
                numeric: true,
                mobile: true,
                cell: (h) => formatCount(h.rejected),
              },
            ]}
            rows={u.data.hours}
            getRowKey={(h) => h.hour}
            empty={{ title: "No request in these days" }}
          />
        </>
      )}
    </section>
  );
}
