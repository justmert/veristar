import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Ban, KeyRound, MoreHorizontal, Plus } from "lucide-react";
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
import { EmptyState, ErrorState, LoadingState, Missing, Skeleton } from "@/components/states";
import { StatusBadge } from "@/components/status-badge";
import { toast } from "@/components/toast";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Menu, MenuContent, MenuItem, MenuTrigger } from "@/components/ui/menu";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
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
  formatDateTimeShort,
  formatDateTimeUtc,
  formatTimeUtc,
} from "@/lib/format";
import { nameTaken } from "@/pages/project/ai";

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

// read-only keys for machines and for ai assistants, on one page: the
// header button opens one form in a dialog that creates either and shows
// the new key once, one table shows and revokes both, and the client guide
// sits under it at the same width
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
  const [creating, setCreating] = useState(false);
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
          <span className="whitespace-nowrap">{formatDateTimeShort(r.lastUsed)}</span>
        ) : (
          <Missing reason="Not used yet" />
        ),
    },
    {
      key: "until",
      header: "Expires",
      // the cell holds only the value; the status column says active,
      // expired or revoked
      cell: (r) =>
        r.revoked ? (
          <Missing
            reason={`Revoked ${formatDate(r.revoked)}${r.revokedReason ? `, ${r.revokedReason}` : ""}`}
          />
        ) : r.expires ? (
          <span className="whitespace-nowrap">{formatDateTimeShort(r.expires)}</span>
        ) : (
          <span className="whitespace-nowrap">Renewed by the client</span>
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

  const tenantOptions = tenants.data?.map((t) => ({ id: t.id, name: t.name })) ?? [];
  const closeCreate = () => {
    setCreating(false);
    setCreated(null);
  };

  return (
    <div className="flex min-w-0 flex-col gap-8">
      <PageHeader
        title="Access keys"
        primary={
          <Button onClick={() => setCreating(true)} className="max-lg:h-(--vs-control-touch)">
            <Plus aria-hidden />
            Create key
          </Button>
        }
      />

      {machines.isPending || assistants.isPending ? (
        <LoadingState label="Loading the keys">
          <Skeleton.Table rows={3} columns={6} />
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
      ) : rows.length === 0 ? (
        <EmptyState icon={<KeyRound aria-hidden strokeWidth={1.75} />} title="No key yet" compact />
      ) : (
        <DataTable
          label="Keys and connections"
          columns={columns}
          rows={rows}
          getRowKey={(r) => r.id}
          rowLabel={(r) => r.name}
          rowProps={(r) => ({ "data-key-id": r.id, "data-kind": r.kind })}
        />
      )}

      <ConnectSteps />

      <Dialog
        open={creating || created !== null}
        onOpenChange={(o) => {
          // a shown key closes only through its done step
          if (!o && created === null) closeCreate();
        }}
        title={created ? "Key created" : "Create a key"}
      >
        {created ? (
          <div className="pb-4">
            <CreatedKey
              created={created}
              tenantName={
                created.type === "machine" && created.key.tenant_id
                  ? (names.get(created.key.tenant_id) ?? null)
                  : null
              }
              onDone={closeCreate}
            />
          </div>
        ) : creating ? (
          <CreateKeyForm
            tenants={tenantOptions}
            tenantsError={tenants.error}
            takenNames={rows.filter((r) => r.status === "active").map((r) => r.name)}
            onCancel={closeCreate}
            onCreated={(c) => {
              setCreating(false);
              setCreated(c);
            }}
          />
        ) : null}
      </Dialog>

      <Dialog
        open={usage !== null}
        onOpenChange={(o) => {
          if (!o) setUsage(null);
        }}
        title={usage ? `Usage of ${usage.name}` : ""}
      >
        {usage ? <Usage row={usage} /> : null}
      </Dialog>

      <ConfirmDialog
        open={revoking !== null}
        onOpenChange={(o) => {
          if (!o) {
            setRevoking(null);
            revoke.reset();
          }
        }}
        title={revoking ? `Revoke the key “${revoking.name}”?` : "Revoke the key?"}
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
  takenNames,
  onCancel,
  onCreated,
}: {
  tenants: { id: string; name: string }[];
  tenantsError: unknown;
  // the names of the live keys, to warn on a second key with the same name
  takenNames: string[];
  onCancel: () => void;
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
    <form
      aria-label="Create a key"
      onSubmit={submit}
      noValidate
      className="flex flex-col gap-4 pb-4"
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
            nameTaken(takenNames, form.name) ? (
              <span className="text-warning">
                A key with this name exists. Use another name to tell them apart.
              </span>
            ) : machine ? (
              "Who uses the key, for example warehouse."
            ) : (
              "Where you use the key, for example laptop."
            )
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
      <div className="flex flex-wrap justify-end gap-2">
        <Button
          type="button"
          variant="ghost"
          onClick={onCancel}
          className="max-lg:h-(--vs-control-touch)"
        >
          Cancel
        </Button>
        <Button type="submit" pending={create.isPending} className="max-lg:h-(--vs-control-touch)">
          {create.isPending ? null : <KeyRound aria-hidden />}
          {create.isPending ? "Creating" : "Create key"}
        </Button>
      </div>
    </form>
  );
}

// a key as the one-time panel shows it before reveal: its prefix, then dots.
// a key is printed in full only inside that panel.
function masked(key: string): string {
  return `${key.slice(0, Math.min(8, key.indexOf("_", 3) + 1 || 4))}${"•".repeat(24)}`;
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
    const curl = (key: string) =>
      `curl -H "Authorization: Bearer ${key}" \\\n  "${origin}/api/metrics/v1/tenants"`;
    return (
      <SecretPanel
        value={k.key}
        noun="key"
        name={k.name}
        description={`The service keeps only a hash, so it shows the key once. It reads ${
          k.tenant_id ? `the project ${tenantName ?? "you chose"}` : "every project"
        }. It expires ${formatDateTimeUtc(k.expires_at)}.`}
        copyLabel="Copy key"
        onDone={onDone}
      >
        <CodeBlock
          code={curl(masked(k.key))}
          copyText={curl(k.key)}
          label="Try it with curl. The copied command holds the key."
          copyLabel="Copy command"
          wrap
        />
      </SecretPanel>
    );
  }
  const k = created.key;
  // the snippets show the key masked; their copy holds the key
  const s = clientSnippets(origin, k.key);
  const shown = clientSnippets(origin, masked(k.key));
  return (
    <SecretPanel
      value={k.key}
      noun="key"
      name={k.name}
      description={`The service keeps only a hash, so it shows the key once. It expires ${
        k.expires_at ? formatDateTimeUtc(k.expires_at) : "never"
      }.`}
      copyLabel="Copy key"
      onDone={onDone}
    >
      <CodeBlock
        code={shown.claudeCode}
        copyText={s.claudeCode}
        label="Claude Code. The copied command holds the key."
        copyLabel="Copy command"
        wrap
      />
      <CodeBlock
        code={shown.cursor}
        copyText={s.cursor}
        label="Cursor, in .cursor/mcp.json. The copied text holds the key."
        copyLabel="Copy configuration"
      />
    </SecretPanel>
  );
}

// how to connect an ai client: the server address once, then each
// client as a tab of numbered steps
function ConnectSteps() {
  const origin = window.location.origin;
  const s = clientSnippets(origin, null);
  return (
    <section aria-labelledby="connect-h" className="flex min-w-0 flex-col gap-4">
      <h2 id="connect-h" className="text-title text-fg">
        Connect an AI assistant
      </h2>
      <div className="flex min-w-0 flex-col gap-1">
        <span className="text-caption text-fg-2">Server address</span>
        <CopyField value={mcpEndpoint(origin)} label="server address" className="min-w-0" />
      </div>
      <Tabs defaultValue="connector" className="min-w-0 gap-4">
        <TabsList aria-label="Client" className="max-w-full overflow-x-auto">
          <TabsTrigger value="connector" className="max-lg:h-(--vs-control-touch)">
            Claude and ChatGPT
          </TabsTrigger>
          <TabsTrigger value="claude-code" className="max-lg:h-(--vs-control-touch)">
            Claude Code
          </TabsTrigger>
          <TabsTrigger value="cursor" className="max-lg:h-(--vs-control-touch)">
            Cursor
          </TabsTrigger>
        </TabsList>
        <TabsContent value="connector" className="min-w-0 animate-rise-in">
          <Steps>
            <Step n={1}>Settings, Connectors, Add custom connector.</Step>
            <Step n={2}>Paste the server address.</Step>
            <Step n={3}>Sign in and approve the operator view.</Step>
          </Steps>
        </TabsContent>
        <TabsContent value="claude-code" className="min-w-0 animate-rise-in">
          <Steps>
            <Step n={1}>Create an AI assistant key.</Step>
            <Step n={2}>
              Run with the key:
              <CodeBlock code={s.claudeCode} copyLabel="Copy command" wrap />
            </Step>
            <Step n={3}>
              Or sign in through the browser:
              <CodeBlock code={s.claudeCodeOAuth} copyLabel="Copy command" wrap />
            </Step>
          </Steps>
        </TabsContent>
        <TabsContent value="cursor" className="min-w-0 animate-rise-in">
          <Steps>
            <Step n={1}>Create an AI assistant key.</Step>
            <Step n={2}>
              <span>
                Add to <code className="font-mono">.cursor/mcp.json</code>:
              </span>
              <CodeBlock code={s.cursor} copyLabel="Copy configuration" />
            </Step>
            <Step n={3}>Put the key in place of the angle brackets.</Step>
          </Steps>
        </TabsContent>
      </Tabs>
    </section>
  );
}

function Steps({ children }: { children: React.ReactNode }) {
  return <ol className="flex min-w-0 list-none flex-col gap-3">{children}</ol>;
}

function Step({ n, children }: { n: number; children: React.ReactNode }) {
  return (
    <li className="grid min-w-0 grid-cols-[1.5rem_minmax(0,1fr)] gap-2 text-small text-fg">
      <span
        aria-hidden
        className="flex size-6 items-center justify-center rounded-full border border-line-strong font-mono text-caption text-fg-2"
      >
        {n}
      </span>
      <div className="flex min-w-0 flex-col gap-2 pt-1">{children}</div>
    </li>
  );
}

// a machine key's requests per utc hour over the last 7 days
function Usage({ row }: { row: KeyRow }) {
  const u = useQuery(apiKeyUsageQuery(row.id));
  return (
    <section aria-label={`Usage of ${row.name}`} className="flex min-w-0 flex-col gap-3 pb-4">
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
            <span className="text-fg tabular" data-usage="requests">
              {formatCount(u.data.totals.requests)}
            </span>{" "}
            requests,{" "}
            <span className="text-fg tabular" data-usage="rejected">
              {formatCount(u.data.totals.rejected)}
            </span>{" "}
            refused.
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
