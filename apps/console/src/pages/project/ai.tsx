import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Ban, ExternalLink, KeyRound } from "lucide-react";
import { type FormEvent, type ReactNode, useState } from "react";
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
import { ConfirmDialog } from "@/components/confirm-dialog";
import { FormMessage } from "@/components/error-panel";
import { TextField } from "@/components/form-field";
import { Stagger, StaggerItem } from "@/components/motion";
import { Notice } from "@/components/notice";
import { PageHeader } from "@/components/page-header";
import { SecretPanel } from "@/components/secret-panel";
import { EmptyState, ErrorState } from "@/components/states";
import { StatusBadge } from "@/components/status-badge";
import { toast } from "@/components/toast";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
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
  const [created, setCreated] = useState<McpCredentialCreated | null>(null);
  const qc = useQueryClient();
  const canCreate = tenant.status === "active" || tenant.status === "pending";
  const docs = siblingUrl(window.location.hostname, "docs");

  return (
    <div className="flex flex-col gap-8">
      <PageHeader
        {...PAGE_HEADERS.ai}
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

      <div className="flex max-w-content flex-col gap-8">
        <Connect created={created} />

        <section aria-labelledby="mcp-keys-h" className="flex min-w-0 flex-col gap-4">
          <div className="flex flex-col gap-1">
            <h2 id="mcp-keys-h" className="text-title text-fg">
              Access keys and connections
            </h2>
            <p className="max-w-prose text-small text-fg-2">
              A connection acts as the person who made it. It stops when that person leaves the
              project. You can revoke your own; owners can revoke all.
            </p>
          </div>
          {created ? (
            <CreatedKeyPanel created={created} onDone={() => setCreated(null)} />
          ) : canCreate ? (
            <McpKeyCreateForm
              label="Create an access key"
              create={(b) => mcpApi.createTenantKey(tenant.id, b)}
              onCreated={(k) => {
                qc.invalidateQueries({ queryKey: mcpKeys.tenant(tenant.id) });
                setCreated(k);
              }}
            />
          ) : (
            <Notice tone="info" title="No new keys while the project is suspended or removed">
              You can still revoke the keys below.
            </Notice>
          )}
          <div className="rounded-lg border border-line bg-raised">
            <CredentialSection
              list={list}
              canRevoke={(c) => isOwner || c.user_id === me.user.id}
              revoke={(c) => mcpApi.revokeTenantCredential(tenant.id, c.id)}
              invalidate={() => qc.invalidateQueries({ queryKey: mcpKeys.tenant(tenant.id) })}
            />
          </div>
        </section>
      </div>
    </div>
  );
}

// the list states: loading rows, error, empty, rows
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
  if (list.isPending)
    return (
      <div role="status" aria-busy="true" className="flex flex-col">
        <span className="sr-only">Loading keys</span>
        {[0, 1].map((i) => (
          <div
            key={i}
            aria-hidden
            className="flex flex-col gap-2 border-b border-line-subtle px-4 py-3 last:border-b-0"
          >
            <Skeleton className="h-4 w-1/3" />
            <Skeleton className="h-3 w-2/3" />
          </div>
        ))}
      </div>
    );
  if (list.error)
    return (
      <div className="p-4">
        <ErrorState
          title="The connections did not load"
          error={list.error}
          onRetry={() => list.refetch()}
          compact
          className="border-0"
        />
      </div>
    );
  return (
    <CredentialList
      items={list.data ?? []}
      canRevoke={canRevoke}
      revoke={revoke}
      invalidate={invalidate}
    />
  );
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

// how to connect each client: the server url once, then numbered steps per
// client. with a key just made, the commands hold it.
export function Connect({ created }: { created: McpCredentialCreated | null }) {
  const origin = window.location.origin;
  const s = clientSnippets(origin, created?.key ?? null);
  const keyStep = created
    ? "The commands below hold your new key."
    : "Create an access key below. Then the commands hold it.";
  return (
    <section
      aria-labelledby="mcp-connect"
      className="flex min-w-0 flex-col gap-4 rounded-lg border border-line bg-raised p-5 max-sm:p-4"
    >
      <h2 id="mcp-connect" className="text-title text-fg">
        Connect a client
      </h2>
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
        <TabsContent value="connector" className="min-w-0 animate-rise-in">
          <Steps>
            <StepItem n={1}>
              In claude.ai or Claude Desktop, open Settings, then Connectors, then Add custom
              connector. In ChatGPT, open Settings, then Connectors, then Create.
            </StepItem>
            <StepItem n={2}>Paste the server URL. Leave the client credentials empty.</StepItem>
            <StepItem n={3}>
              Sign in to this console, choose the project and approve. You do not need a key.
            </StepItem>
          </Steps>
        </TabsContent>
        <TabsContent value="claude-code" className="min-w-0 animate-rise-in">
          <Steps>
            <StepItem n={1}>{keyStep}</StepItem>
            <StepItem n={2}>
              Run this command.
              <CodeBlock code={s.claudeCode} label="Terminal" copyLabel="Copy command" />
            </StepItem>
            <StepItem n={3}>
              To sign in through the browser instead of a key, run this command.
              <CodeBlock code={s.claudeCodeOAuth} label="Terminal" copyLabel="Copy command" />
            </StepItem>
          </Steps>
        </TabsContent>
        <TabsContent value="cursor" className="min-w-0 animate-rise-in">
          <Steps>
            <StepItem n={1}>{keyStep}</StepItem>
            <StepItem n={2}>
              <span>
                Add this to <code className="font-mono">.cursor/mcp.json</code> in the project, or
                to <code className="font-mono">~/.cursor/mcp.json</code> for all projects.
              </span>
              <CodeBlock code={s.cursor} label="mcp.json" copyLabel="Copy configuration" />
            </StepItem>
          </Steps>
        </TabsContent>
      </Tabs>
    </section>
  );
}

export function McpKeyCreateForm({
  label,
  create,
  onCreated,
}: {
  label: string;
  create: (b: McpKeyInput) => Promise<McpCredentialCreated>;
  onCreated: (k: McpCredentialCreated) => void;
}) {
  const [form, setForm] = useState<McpKeyForm>({ name: "", days: String(MCP_KEY_DEFAULT_DAYS) });
  const [errors, setErrors] = useState<McpKeyErrors>({});
  const m = useMutation({
    mutationFn: create,
    onSuccess: (k) => {
      setForm({ name: "", days: String(MCP_KEY_DEFAULT_DAYS) });
      onCreated(k);
    },
    onError: (err) => setErrors(serverErrors(err)),
  });
  function submit(e: FormEvent) {
    e.preventDefault();
    const v = validateMcpKey(form);
    setErrors(v.errors);
    if (v.body) m.mutate(v.body);
  }
  return (
    <form
      aria-label={label}
      onSubmit={submit}
      noValidate
      className="max-w-prose flex flex-col gap-3 rounded-lg border border-line bg-raised p-5 max-sm:p-4"
    >
      <h3 className="text-body font-medium text-fg">{label}</h3>
      <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_11rem_auto] sm:items-start">
        <TextField
          label="Name"
          hint="Where you use the key, for example laptop or Cursor."
          placeholder="laptop"
          maxLength={100}
          className="max-lg:h-(--vs-control-touch)"
          value={form.name}
          error={errors.name}
          onChange={(e) => setForm({ ...form, name: e.target.value })}
        />
        <TextField
          label="Expires after (days)"
          hint={`${MCP_KEY_DEFAULT_DAYS} by default, at most ${MCP_KEY_MAX_DAYS}.`}
          type="number"
          inputMode="numeric"
          min={1}
          max={MCP_KEY_MAX_DAYS}
          className="tabular max-lg:h-(--vs-control-touch)"
          value={form.days}
          error={errors.days}
          onChange={(e) => setForm({ ...form, days: e.target.value })}
        />
        <Button
          type="submit"
          className="sm:mt-6 max-lg:h-(--vs-control-touch)"
          pending={m.isPending}
        >
          {m.isPending ? null : <KeyRound aria-hidden />}
          {m.isPending ? "Creating" : "Create key"}
        </Button>
      </div>
      {m.error && Object.keys(serverErrors(m.error)).length === 0 ? (
        <FormMessage error={m.error} />
      ) : null}
    </form>
  );
}

export function CreatedKeyPanel({
  created,
  onDone,
}: {
  created: McpCredentialCreated;
  onDone: () => void;
}) {
  return (
    <section aria-label="New access key">
      <SecretPanel
        value={created.key}
        noun="key"
        name={created.name}
        description={`It is shown only once. It expires ${
          created.expires_at ? formatDateTimeUtc(created.expires_at) : "never"
        }. The Claude Code and Cursor commands above now hold it.`}
        onDone={onDone}
        copyLabel="Copy key"
      />
    </section>
  );
}

export function CredentialList({
  items,
  canRevoke,
  revoke,
  invalidate,
}: {
  items: McpCredential[];
  canRevoke: (c: McpCredential) => boolean;
  revoke: (c: McpCredential) => Promise<void>;
  invalidate: () => void;
}) {
  if (items.length === 0)
    return (
      <div className="p-4">
        <EmptyState
          compact
          className="border-0"
          icon={<KeyRound aria-hidden strokeWidth={1.75} />}
          title="No key or connection yet"
        >
          Create an access key, or connect a client through sign-in.
        </EmptyState>
      </div>
    );
  return (
    <Stagger as="ul" aria-label="MCP keys and connections" className="flex flex-col">
      {items.map((c) => (
        <CredentialRow
          key={c.id}
          c={c}
          canRevoke={canRevoke(c)}
          revoke={() => revoke(c)}
          invalidate={invalidate}
        />
      ))}
    </Stagger>
  );
}

// the sentence under a key: who it acts as, its dates and its last use
export function credentialLine(c: McpCredential): string {
  const parts = [
    c.display_name ? `Acts as ${c.display_name}.` : "",
    `Created ${formatDateTimeUtc(c.created_at)}.`,
    c.revoked_at
      ? `Revoked ${formatDateTimeUtc(c.revoked_at)}${
          c.revoked_reason ? `: ${REVOKED_REASON[c.revoked_reason] ?? c.revoked_reason}` : ""
        }.`
      : c.expires_at
        ? `${c.status === "expired" ? "Expired" : "Expires"} ${formatDateTimeUtc(c.expires_at)}.`
        : "Stays valid while the client refreshes it.",
    c.last_seen_at ? `Last used ${formatDateTimeUtc(c.last_seen_at)}.` : "Not used yet.",
  ];
  return parts.filter(Boolean).join(" ");
}

function CredentialRow({
  c,
  canRevoke,
  revoke,
  invalidate,
}: {
  c: McpCredential;
  canRevoke: boolean;
  revoke: () => Promise<void>;
  invalidate: () => void;
}) {
  const [confirm, setConfirm] = useState(false);
  const m = useMutation({
    mutationFn: revoke,
    onSuccess: () => {
      setConfirm(false);
      toast.success(`${c.name} is revoked.`);
      invalidate();
    },
  });
  const kind = c.kind === "key" ? "Access key" : "Connector";
  return (
    <StaggerItem
      as="li"
      className="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-line-subtle px-4 py-3 transition-colors duration-(--vs-duration-press) ease-standard last:border-b-0 hover:bg-hover"
      data-credential-id={c.id}
      aria-label={`${kind} ${c.name}`}
    >
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <span className="min-w-0 truncate text-body font-medium text-fg">{c.name}</span>
          <StatusBadge kind="key" status={c.status} />
          <span className="text-caption text-fg-2">{kind}</span>
          {c.prefix ? (
            <code className="font-mono text-caption text-fg-2">{c.prefix}...</code>
          ) : null}
        </div>
        <p className="text-small text-fg-2">{credentialLine(c)}</p>
      </div>
      {c.status === "active" && canRevoke ? (
        <Button
          variant="destructive-outline"
          size="sm"
          className="max-lg:h-(--vs-control-touch)"
          onClick={() => {
            m.reset();
            setConfirm(true);
          }}
        >
          <Ban aria-hidden />
          Revoke
        </Button>
      ) : null}
      <ConfirmDialog
        open={confirm}
        onOpenChange={setConfirm}
        title={`Revoke the key “${c.name}”?`}
        description="The next request of this client is refused. You cannot restore it. Make a new one instead."
        confirmLabel="Revoke"
        pendingLabel="Revoking"
        pending={m.isPending}
        error={m.error}
        onConfirm={() => m.mutate()}
      />
    </StaggerItem>
  );
}
