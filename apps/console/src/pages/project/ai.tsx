import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Ban, ExternalLink, KeyRound } from "lucide-react";
import { type ReactNode, useState } from "react";
import { ApiError } from "@/api/client";
import {
  clientSnippets,
  MCP_KEY_DEFAULT_DAYS,
  MCP_KEY_MAX_DAYS,
  type McpCredential,
  type McpCredentialCreated,
  type McpKeyInput,
  mcpApi,
  mcpEndpoint,
  mcpKeys,
  REVOKED_REASON,
  tenantMcpQuery,
} from "@/api/mcp";
import { CodeBlock, CopyField } from "@/components/code-block";
import { CreateDialog } from "@/components/create-dialog";
import { type Column, DataTable } from "@/components/data-table";
import { TextField } from "@/components/form-field";
import { Notice } from "@/components/notice";
import { PageHeader } from "@/components/page-header";
import { Section } from "@/components/panel";
import { Missing } from "@/components/states";
import { StatusBadge } from "@/components/status-badge";
import { toast } from "@/components/toast";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { formatDateTimeUtc } from "@/lib/format";
import { siblingUrl } from "@/lib/hosts";
import { useProject } from "@/pages/project-layout";
import { PAGE_HEADERS } from "./page-frame";

// the project's ai assistants page (contract 12.8): the server url, the
// steps for each client, access keys, and the list of everything that can
// read this project through mcp. the operator access keys page uses the same
// parts with operator keys.

export type McpKeyForm = { name: string; days: string };
export type McpKeyErrors = Partial<Record<keyof McpKeyForm, string>>;

function isControl(ch: string): boolean {
  const c = ch.codePointAt(0) ?? 0;
  return c < 0x20 || c === 0x7f;
}

// the field rules of 12.8, checked before sending; the server checks again
export function validateMcpKey(f: McpKeyForm): { errors: McpKeyErrors; body: McpKeyInput | null } {
  const errors: McpKeyErrors = {};
  const name = f.name.trim();
  if (!name) errors.name = "Enter a name.";
  else if ([...name].length > 100) errors.name = "Use at most 100 characters.";
  else if ([...name].some(isControl)) errors.name = "Remove control characters.";
  const days = Number(f.days);
  if (!/^\d+$/.test(f.days) || days < 1 || days > MCP_KEY_MAX_DAYS)
    errors.days = `Enter a whole number of days from 1 to ${MCP_KEY_MAX_DAYS}.`;
  if (Object.keys(errors).length) return { errors, body: null };
  return { errors, body: { name, expires_in_days: days } };
}

function serverErrors(err: unknown): McpKeyErrors {
  if (!(err instanceof ApiError) || err.code !== "validation_failed") return {};
  const out: McpKeyErrors = {};
  for (const [field, code] of Object.entries(err.fields)) {
    if (field === "name")
      out.name = code === "too_long" ? "Use at most 100 characters." : "Enter a valid name.";
    if (field === "expires_in_days") out.days = `Enter 1 to ${MCP_KEY_MAX_DAYS} days.`;
  }
  return out;
}

export function AiPage() {
  const { tenant, me, isOwner } = useProject();
  const list = useQuery(tenantMcpQuery(tenant.id));
  const qc = useQueryClient();
  const canCreate = tenant.status === "active" || tenant.status === "pending";
  const docs = siblingUrl(window.location.hostname, "docs");

  return (
    <div className="flex flex-col gap-8">
      <PageHeader
        {...PAGE_HEADERS.ai}
        primary={
          canCreate ? (
            <CreateKey
              takenNames={(list.data ?? []).filter((c) => c.status === "active").map((c) => c.name)}
              create={(b) => mcpApi.createTenantKey(tenant.id, b)}
              onCreated={() => qc.invalidateQueries({ queryKey: mcpKeys.tenant(tenant.id) })}
            />
          ) : null
        }
        secondary={
          docs ? (
            <Button asChild variant="outline">
              <a href={`${docs}/onboarding/mcp`}>
                Guide
                <ExternalLink aria-hidden />
              </a>
            </Button>
          ) : null
        }
      />
      {canCreate ? null : (
        <Notice tone="info" title="No new keys while the project is suspended or removed" />
      )}
      <Section title="Keys and connections" count={list.data?.length ?? null}>
        <CredentialSection
          list={list}
          canRevoke={(c) => isOwner || c.user_id === me.user.id}
          revoke={(c) => mcpApi.revokeTenantCredential(tenant.id, c.id)}
          invalidate={() => qc.invalidateQueries({ queryKey: mcpKeys.tenant(tenant.id) })}
        />
      </Section>
      <Connect created={null} />
    </div>
  );
}

// the header's Create key: a name and an expiry in a dialog, then the key
// once in the same dialog, with the client commands that hold it
export function CreateKey({
  create,
  onCreated,
  takenNames = [],
}: {
  create: (b: McpKeyInput) => Promise<McpCredentialCreated>;
  onCreated: (k: McpCredentialCreated) => void;
  // the names of the live keys, to warn on a second key with the same name
  takenNames?: string[];
}) {
  const [form, setForm] = useState<McpKeyForm>({ name: "", days: String(MCP_KEY_DEFAULT_DAYS) });
  const [errors, setErrors] = useState<McpKeyErrors>({});
  const m = useMutation({
    mutationFn: create,
    onSuccess: (k) => onCreated(k),
    onError: (err) => setErrors(serverErrors(err)),
  });
  const created = m.data;
  return (
    <CreateDialog
      label="Create key"
      title="Create an access key"
      icon={<KeyRound aria-hidden />}
      pending={m.isPending}
      pendingLabel="Creating"
      error={m.error && Object.keys(serverErrors(m.error)).length === 0 ? m.error : null}
      onSubmit={() => {
        const v = validateMcpKey(form);
        setErrors(v.errors);
        if (v.body) m.mutate(v.body);
      }}
      onClosed={() => {
        m.reset();
        setForm({ name: "", days: String(MCP_KEY_DEFAULT_DAYS) });
        setErrors({});
      }}
      secret={
        created
          ? {
              value: created.key,
              noun: "key",
              name: created.name,
              copyLabel: "Copy key",
              children: <ClientCommands created={created} />,
            }
          : null
      }
    >
      <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_11rem]">
        <TextField
          label="Name"
          hint={
            nameTaken(takenNames, form.name) ? (
              <span className="text-warning">
                A key with this name exists. Use another name to tell them apart.
              </span>
            ) : undefined
          }
          placeholder="laptop"
          maxLength={100}
          className="max-lg:h-(--vs-control-touch)"
          value={form.name}
          error={errors.name}
          onChange={(e) => setForm({ ...form, name: e.target.value })}
        />
        <TextField
          label="Expires after (days)"
          hint={`At most ${MCP_KEY_MAX_DAYS}`}
          type="number"
          inputMode="numeric"
          min={1}
          max={MCP_KEY_MAX_DAYS}
          className="tabular max-lg:h-(--vs-control-touch)"
          value={form.days}
          error={errors.days}
          onChange={(e) => setForm({ ...form, days: e.target.value })}
        />
      </div>
    </CreateDialog>
  );
}

// the commands of the clients that take a key, written with the new key;
// the code on screen shows its start, the copy holds it whole
function ClientCommands({ created }: { created: McpCredentialCreated }) {
  const origin = window.location.origin;
  const s = clientSnippets(origin, created.key);
  const shown = clientSnippets(origin, `${created.key.slice(0, 11)}...`);
  return (
    <Tabs defaultValue="claude-code" className="min-w-0 gap-3">
      <TabsList aria-label="Client" className="max-w-full overflow-x-auto">
        <TabsTrigger value="claude-code">Claude Code</TabsTrigger>
        <TabsTrigger value="cursor">Cursor</TabsTrigger>
      </TabsList>
      <TabsContent value="claude-code" className="min-w-0">
        <CodeBlock
          code={shown.claudeCode}
          copyText={s.claudeCode}
          label="Terminal"
          copyLabel="Copy command"
          wrap
        />
      </TabsContent>
      <TabsContent value="cursor" className="min-w-0">
        <CodeBlock
          code={shown.cursor}
          copyText={s.cursor}
          label="mcp.json"
          copyLabel="Copy configuration"
        />
      </TabsContent>
    </Tabs>
  );
}

// keys and connections in the same table form as the api tokens: name,
// status, created, ends, last used, revoke. the table draws the loading,
// error and empty states.
export function CredentialSection({
  list,
  canRevoke,
  revoke,
  invalidate,
}: {
  list: { isPending: boolean; error: unknown; data?: McpCredential[]; refetch: () => unknown };
  canRevoke: (c: McpCredential) => boolean;
  revoke: (c: McpCredential) => Promise<void>;
  invalidate: () => void;
}) {
  const columns: Column<McpCredential>[] = [
    {
      key: "name",
      header: "Name",
      primary: true,
      minWidth: "min-w-56",
      className: "w-full",
      cell: (c) => (
        <span className="flex min-w-0 flex-col py-1">
          <span className="font-medium break-words text-fg">{c.name}</span>
          <span className="truncate text-caption font-normal text-fg-2">
            {c.kind === "key" ? "Access key" : "Connector"}
            {c.prefix ? <code className="ml-2 font-mono">{c.prefix}...</code> : null}
            {c.display_name ? `, acts as ${c.display_name}` : ""}
          </span>
        </span>
      ),
    },
    {
      key: "status",
      header: "Status",
      mobile: true,
      cell: (c) => <StatusBadge kind="key" status={c.status} />,
    },
    {
      key: "created",
      header: "Created",
      cell: (c) => (
        <span className="whitespace-nowrap text-fg-2">{formatDateTimeUtc(c.created_at)}</span>
      ),
    },
    {
      key: "ends",
      header: "Ends",
      minWidth: "min-w-44",
      cell: (c) => <span className="text-fg-2">{credentialEnd(c)}</span>,
    },
    {
      key: "used",
      header: "Last used",
      mobile: true,
      cell: (c) =>
        c.last_seen_at ? (
          <span className="whitespace-nowrap text-fg-2">{formatDateTimeUtc(c.last_seen_at)}</span>
        ) : (
          <Missing reason={NOT_USED} />
        ),
    },
  ];
  return (
    <>
      <DataTable<McpCredential>
        label="MCP keys and connections"
        columns={columns}
        rows={list.data ?? []}
        getRowKey={(c) => c.id}
        rowLabel={(c) => c.name}
        rowProps={(c) => ({ "data-credential-id": c.id })}
        state={list.isPending ? "loading" : list.error ? "error" : "data"}
        error={list.error}
        onRetry={() => list.refetch()}
        loadingRows={2}
        empty={{ title: "No key or connection yet" }}
        rowActions={(c) => [
          c.status === "active" &&
            canRevoke(c) && {
              label: "Revoke",
              icon: <Ban aria-hidden />,
              destructive: true,
              confirm: {
                title: `Revoke the key “${c.name}”?`,
                description:
                  "The next request of this client is refused. You cannot restore it. Make a new one instead.",
                confirmLabel: "Revoke",
                pendingLabel: "Revoking",
                run: async () => {
                  await revoke(c);
                  toast.success(`${c.name} is revoked.`);
                  invalidate();
                },
              },
            },
        ]}
      />
    </>
  );
}

// one wording for a credential that never sent a request, on this page and
// on Connect API
export const NOT_USED = "Not used yet";

// when a credential stops: revoked with the reason, expired, expires, or
// kept alive by its client
export function credentialEnd(c: McpCredential): string {
  if (c.revoked_at)
    return `Revoked ${formatDateTimeUtc(c.revoked_at)}${
      c.revoked_reason ? `: ${REVOKED_REASON[c.revoked_reason] ?? c.revoked_reason}` : ""
    }`;
  if (c.expires_at)
    return `${c.status === "expired" ? "Expired" : "Expires"} ${formatDateTimeUtc(c.expires_at)}`;
  return "Stays valid while the client refreshes it";
}

// numbered steps of one client
function Steps({ children }: { children: ReactNode }) {
  return <ol className="flex min-w-0 list-none flex-col gap-3">{children}</ol>;
}

function StepItem({ n, children }: { n: number; children: ReactNode }) {
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

// how to connect each client: the server url once, then a few numbered
// steps per client. a key's commands are in the create dialog, filled in
export function Connect({ created }: { created: McpCredentialCreated | null }) {
  const origin = window.location.origin;
  const s = clientSnippets(origin, created?.key ?? null);
  return (
    <Section title="Connect a client">
      <div className="flex min-w-0 flex-col gap-1">
        <span className="text-caption text-fg-2">Server URL</span>
        <CopyField value={mcpEndpoint(origin)} label="server URL" className="max-w-md" />
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
        <TabsContent value="connector" className="min-w-0">
          <Steps>
            <StepItem n={1}>Settings, then Connectors</StepItem>
            <StepItem n={2}>Add a custom connector with the server URL</StepItem>
            <StepItem n={3}>Sign in and approve</StepItem>
          </Steps>
        </TabsContent>
        <TabsContent value="claude-code" className="min-w-0">
          <Steps>
            <StepItem n={1}>Create a key</StepItem>
            <StepItem n={2}>
              Run the command
              <CodeBlock code={s.claudeCode} label="Terminal" copyLabel="Copy command" wrap />
            </StepItem>
            <StepItem n={3}>
              Or sign in without a key
              <CodeBlock code={s.claudeCodeOAuth} label="Terminal" copyLabel="Copy command" wrap />
            </StepItem>
          </Steps>
        </TabsContent>
        <TabsContent value="cursor" className="min-w-0">
          <Steps>
            <StepItem n={1}>Create a key</StepItem>
            <StepItem n={2}>
              Add to <code className="font-mono">.cursor/mcp.json</code>
              <CodeBlock code={s.cursor} label="mcp.json" copyLabel="Copy configuration" />
            </StepItem>
          </Steps>
        </TabsContent>
      </Tabs>
    </Section>
  );
}

// two live credentials with one name can only be told apart by their
// prefix, so the form warns before it creates a second one
export function nameTaken(names: string[], name: string): boolean {
  const n = name.trim().toLowerCase();
  return n !== "" && names.some((x) => x.trim().toLowerCase() === n);
}
