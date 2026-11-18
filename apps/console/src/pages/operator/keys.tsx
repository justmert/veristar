import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Ban, KeyRound } from "lucide-react";
import { useState } from "react";
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
import { CreateDialog } from "@/components/create-dialog";
import { type Column, DataTable } from "@/components/data-table";
import { FormMessage } from "@/components/error-panel";
import { SelectField, TextField } from "@/components/form-field";
import { PageHeader } from "@/components/page-header";
import { EmptyState, ErrorState, LoadingState, Missing, Skeleton } from "@/components/states";
import { StatusBadge } from "@/components/status-badge";
import { toast } from "@/components/toast";
import { Dialog } from "@/components/ui/dialog";
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
  const [usage, setUsage] = useState<KeyRow | null>(null);
  const names = new Map((tenants.data ?? []).map((t) => [t.id, t.name]));

  const rows: KeyRow[] = [
    ...(machines.data ?? []).map((k) => machineRow(k, names)),
    ...(assistants.data ?? []).map(assistantRow),
  ].sort(
    (a, b) => STATUS_ORDER[a.status] - STATUS_ORDER[b.status] || b.created.localeCompare(a.created),
  );
  const error = machines.error ?? assistants.error;

  async function revoke(r: KeyRow) {
    await (r.kind === "machine"
      ? globalApi.revokeApiKey(r.id)
      : mcpApi.revokeOperatorCredential(r.id));
    toast.success(`${r.name} is revoked. Its next request is refused.`);
    await Promise.all([
      qc.invalidateQueries({ queryKey: keys.opKeys }),
      qc.invalidateQueries({ queryKey: mcpKeys.operator }),
    ]);
  }

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
  ];

  return (
    <div className="flex min-w-0 flex-col gap-8">
      <PageHeader
        title="Access keys"
        primary={
          <CreateKey
            tenants={tenants.data?.map((t) => ({ id: t.id, name: t.name })) ?? []}
            tenantsError={tenants.error}
            takenNames={rows.filter((r) => r.status === "active").map((r) => r.name)}
            names={names}
          />
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
          rowActions={(r) => [
            r.kind === "machine" ? { label: "Show usage", onSelect: () => setUsage(r) } : null,
            r.status !== "revoked"
              ? {
                  label: "Revoke",
                  icon: <Ban aria-hidden />,
                  destructive: true,
                  confirm: {
                    title: `Revoke the key “${r.name}”?`,
                    description:
                      "The next request with it is refused. You cannot restore it. Create a new key instead.",
                    confirmLabel: "Revoke",
                    pendingLabel: "Revoking",
                    run: () => revoke(r),
                  },
                }
              : null,
          ]}
        />
      )}

      <ConnectSteps />

      <Dialog
        open={usage !== null}
        onOpenChange={(o) => {
          if (!o) setUsage(null);
        }}
        title={usage ? `Usage of ${usage.name}` : ""}
      >
        {usage ? <Usage row={usage} /> : null}
      </Dialog>
    </div>
  );
}

type Created =
  | { type: "machine"; key: ApiKeyCreated }
  | { type: "assistant"; key: McpCredentialCreated };

// the header button and its dialog: one form for both kinds of key, then
// the new key once, with its first command, in the same dialog
function CreateKey({
  tenants,
  tenantsError,
  takenNames,
  names,
}: {
  tenants: { id: string; name: string }[];
  tenantsError: unknown;
  // the names of the live keys, to warn on a second key with the same name
  takenNames: string[];
  names: Map<string, string>;
}) {
  const qc = useQueryClient();
  const [form, setForm] = useState<KeyForm>(EMPTY_KEY_FORM);
  const [errors, setErrors] = useState<KeyErrors>({});
  const [created, setCreated] = useState<Created | null>(null);
  const create = useMutation({
    mutationFn: async (
      b: NonNullable<ReturnType<typeof validateKeyForm>["body"]>,
    ): Promise<Created> =>
      b.type === "machine"
        ? { type: "machine", key: await globalApi.createApiKey(b.body) }
        : { type: "assistant", key: await mcpApi.createOperatorKey(b.body) },
    onSuccess: async (c) => {
      await qc.invalidateQueries({
        queryKey: c.type === "machine" ? keys.opKeys : mcpKeys.operator,
      });
      setCreated(c);
    },
    onError: (err) => setErrors(serverKeyErrors(err)),
  });

  function submit() {
    const v = validateKeyForm(form);
    setErrors(v.errors);
    if (v.body) create.mutate(v.body);
  }

  const machine = form.type === "machine";
  return (
    <CreateDialog
      label="Create key"
      title="Create a key"
      pending={create.isPending}
      pendingLabel="Creating"
      error={
        create.error && Object.keys(serverKeyErrors(create.error)).length === 0
          ? create.error
          : null
      }
      onSubmit={submit}
      secret={
        created
          ? {
              value: created.key.key,
              noun: "key",
              name: created.key.name,
              copyLabel: "Copy key",
              children: (
                <CreatedKey
                  created={created}
                  tenantName={
                    created.type === "machine" && created.key.tenant_id
                      ? (names.get(created.key.tenant_id) ?? null)
                      : null
                  }
                />
              ),
            }
          : null
      }
      onClosed={() => {
        setForm(EMPTY_KEY_FORM);
        setErrors({});
        setCreated(null);
        create.reset();
      }}
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
            description="For a script or a job that reads the metrics API."
          />
          <ChoiceCard
            name="key-type"
            value="assistant"
            checked={!machine}
            onChange={() => setForm({ ...form, type: "assistant" })}
            title="AI assistant key"
            description="For an AI client that searches and compares projects as you."
          />
        </div>
      </fieldset>
      <div className="grid gap-4 sm:grid-cols-[minmax(0,1fr)_11rem]">
        <TextField
          label="Name"
          hint={
            nameTaken(takenNames, form.name) ? (
              <span className="text-warning">A key with this name exists.</span>
            ) : undefined
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
          placeholder={`${DEFAULT_EXPIRY_DAYS}, at most ${MAX_EXPIRY_DAYS}`}
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
            />
            <ChoiceCard
              name="scope"
              value="tenant"
              checked={form.scope === "tenant"}
              onChange={() => setForm({ ...form, scope: "tenant" })}
              title="One project"
            />
          </div>
          {form.scope === "tenant" ? (
            <div className="flex animate-rise-in flex-col gap-2 pt-2">
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
    </CreateDialog>
  );
}

// a key as the one-time panel shows it before reveal: its prefix, then dots.
// a key is printed in full only inside that panel.
function masked(key: string): string {
  return `${key.slice(0, Math.min(8, key.indexOf("_", 3) + 1 || 4))}${"•".repeat(24)}`;
}

// the first command with the new key: shown masked, copied in full
function CreatedKey({ created, tenantName }: { created: Created; tenantName: string | null }) {
  const origin = window.location.origin;
  if (created.type === "machine") {
    const k = created.key;
    const curl = (key: string) =>
      `curl -H "Authorization: Bearer ${key}" \\\n  "${origin}/api/metrics/v1/tenants"`;
    return (
      <div className="flex min-w-0 flex-col gap-3">
        <p className="text-small text-fg-2">
          {k.tenant_id ? (tenantName ?? "One project") : "Every project"}, until{" "}
          {formatDateTimeUtc(k.expires_at)}
        </p>
        <CodeBlock
          code={curl(masked(k.key))}
          copyText={curl(k.key)}
          label="curl"
          copyLabel="Copy command"
          wrap
        />
      </div>
    );
  }
  const k = created.key;
  // the snippets show the key masked; their copy holds the key
  const s = clientSnippets(origin, k.key);
  const shown = clientSnippets(origin, masked(k.key));
  return (
    <div className="flex min-w-0 flex-col gap-3">
      {k.expires_at ? (
        <p className="text-small text-fg-2">Until {formatDateTimeUtc(k.expires_at)}</p>
      ) : null}
      <CodeBlock
        code={shown.claudeCode}
        copyText={s.claudeCode}
        label="Claude Code"
        copyLabel="Copy command"
        wrap
      />
      <CodeBlock
        code={shown.cursor}
        copyText={s.cursor}
        label=".cursor/mcp.json"
        copyLabel="Copy configuration"
      />
    </div>
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
