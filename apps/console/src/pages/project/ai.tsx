import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { Ban, Lock } from "lucide-react";
import { type ReactNode, useId, useState } from "react";
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
import { type Column, DataTable } from "@/components/data-table";
import { FormMessage } from "@/components/error-panel";
import { Notice } from "@/components/notice";
import { PageHeader } from "@/components/page-header";
import { Section } from "@/components/panel";
import { SecretPanel } from "@/components/secret-panel";
import { StatusBadge } from "@/components/status-badge";
import { toast } from "@/components/toast";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { formatDate, formatDateTimeUtc } from "@/lib/format";
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

type Client = "connector" | "claude-code" | "cursor";

const CLIENTS: { id: Client; name: string; how: string }[] = [
  { id: "connector", name: "Claude and ChatGPT", how: "sign in, no key" },
  { id: "claude-code", name: "Claude Code", how: "terminal, with a key" },
  { id: "cursor", name: "Cursor", how: "mcp.json, with a key" },
];

export function AiPage() {
  const { tenant, me, isOwner } = useProject();
  const list = useQuery(tenantMcpQuery(tenant.id));
  const qc = useQueryClient();
  const canCreate = tenant.status === "active" || tenant.status === "pending";
  const docs = siblingUrl(window.location.hostname, "docs");
  const [client, setClient] = useState<Client>("connector");
  const [showRevoked, setShowRevoked] = useState(false);
  const revokedId = useId();
  // a key made here is shown once, then only its start
  const [created, setCreated] = useState<McpCredentialCreated | null>(null);
  const all = list.data ?? [];
  const rows = showRevoked ? all : all.filter((c) => c.status !== "revoked");

  return (
    <div className="flex flex-col gap-7">
      <PageHeader
        title={PAGE_HEADERS.ai.title}
        meta={
          <span className="inline-flex h-6 items-center gap-1.5 rounded-full border border-line-strong px-2 text-label text-fg-2">
            <Lock aria-hidden className="size-3" />
            Read-only, this project
          </span>
        }
        secondary={
          docs ? (
            <Button asChild variant="ghost">
              <a href={`${docs}/onboarding/mcp`}>Guide</a>
            </Button>
          ) : null
        }
      />

      <Section title="Connect a client">
        <div className="grid min-w-0 overflow-hidden rounded-2xl border border-line bg-raised md:grid-cols-[260px_minmax(0,1fr)]">
          <div
            role="radiogroup"
            aria-label="Client"
            className="flex flex-col gap-1 border-line p-3 max-md:border-b md:border-r"
          >
            {CLIENTS.map((c, i) => {
              const on = c.id === client;
              return (
                // biome-ignore lint/a11y/useSemanticElements: a client card keyed like a native radio group
                <button
                  key={c.id}
                  type="button"
                  role="radio"
                  aria-checked={on}
                  tabIndex={on ? 0 : -1}
                  onClick={() => setClient(c.id)}
                  onKeyDown={(e) => {
                    const d =
                      e.key === "ArrowDown" || e.key === "ArrowRight"
                        ? 1
                        : e.key === "ArrowUp" || e.key === "ArrowLeft"
                          ? -1
                          : 0;
                    if (!d) return;
                    e.preventDefault();
                    const j = (i + d + CLIENTS.length) % CLIENTS.length;
                    const next = CLIENTS[j];
                    if (!next) return;
                    setClient(next.id);
                    (
                      e.currentTarget.parentElement?.children[j] as HTMLElement | undefined
                    )?.focus();
                  }}
                  className={cn(
                    "flex flex-col gap-1 rounded-xl border px-3.5 py-3 text-left transition-colors duration-(--vs-duration-press) ease-standard focus-ring",
                    on ? "border-line-focus bg-hover" : "border-transparent hover:bg-hover",
                  )}
                >
                  <span className="text-body font-medium text-fg">{c.name}</span>
                  <span className="text-small text-fg-3">{c.how}</span>
                </button>
              );
            })}
          </div>
          <div className="min-w-0 p-5 max-sm:p-4">
            <ClientSteps
              client={client}
              tenantName={tenant.name}
              canCreate={canCreate}
              created={created}
              takenNames={all.filter((c) => c.status === "active").map((c) => c.name)}
              create={(b) => mcpApi.createTenantKey(tenant.id, b)}
              onCreated={(k) => {
                setCreated(k);
                void qc.invalidateQueries({ queryKey: mcpKeys.tenant(tenant.id) });
              }}
              onDone={() => setCreated(null)}
            />
          </div>
        </div>
      </Section>

      <Section
        title="Keys and connections"
        count={list.data ? rows.length : null}
        actions={
          <span className="flex items-center gap-2">
            <Checkbox
              id={revokedId}
              checked={showRevoked}
              onCheckedChange={(c) => setShowRevoked(c === true)}
            />
            <label htmlFor={revokedId} className="text-small text-fg-2">
              Show revoked
            </label>
          </span>
        }
      >
        <CredentialSection
          list={{ ...list, data: list.data ? rows : undefined }}
          canRevoke={(c) => isOwner || c.user_id === me.user.id}
          revoke={(c) => mcpApi.revokeTenantCredential(tenant.id, c.id)}
          invalidate={() => qc.invalidateQueries({ queryKey: mcpKeys.tenant(tenant.id) })}
        />
      </Section>
    </div>
  );
}

// the numbered steps of one client: the server address and the approval
// for the connector clients, a key and its command for the others
function ClientSteps({
  client,
  tenantName,
  canCreate,
  created,
  takenNames,
  create,
  onCreated,
  onDone,
}: {
  client: Client;
  tenantName: string;
  canCreate: boolean;
  created: McpCredentialCreated | null;
  takenNames: string[];
  create: (b: McpKeyInput) => Promise<McpCredentialCreated>;
  onCreated: (k: McpCredentialCreated) => void;
  onDone: () => void;
}) {
  const origin = window.location.origin;
  const s = clientSnippets(origin, created?.key ?? null);
  const shown = clientSnippets(origin, created ? `${created.key.slice(0, 11)}...` : null);
  if (client === "connector")
    return (
      <Steps>
        <StepItem n={1} title="Copy the server address">
          <CopyField value={mcpEndpoint(origin)} label="server address" />
        </StepItem>
        <StepItem n={2} title="Add it as a custom connector">
          <span className="text-small text-fg-2">Settings, Connectors, Add custom connector</span>
        </StepItem>
        <StepItem n={3} title={`Sign in to Veristar and approve ${tenantName}`} />
      </Steps>
    );
  const keyStep = (
    <StepItem n={1} title="Create a key for this client">
      {!canCreate ? (
        <Notice tone="info" title="No new keys while the project is suspended or removed" />
      ) : created ? (
        <SecretPanel
          plain
          value={created.key}
          noun="key"
          name={created.name}
          copyLabel="Copy key"
          onDone={onDone}
        />
      ) : (
        <CreateKey create={create} onCreated={onCreated} takenNames={takenNames} />
      )}
    </StepItem>
  );
  if (client === "claude-code")
    return (
      <Steps>
        {keyStep}
        <StepItem n={2} title="Run in the terminal">
          <CodeBlock
            code={shown.claudeCode}
            copyText={s.claudeCode}
            label="Terminal"
            copyLabel="Copy command"
            wrap
          />
        </StepItem>
        <StepItem n={3} title="Or sign in without a key">
          <CodeBlock code={s.claudeCodeOAuth} label="Terminal" copyLabel="Copy command" wrap />
        </StepItem>
      </Steps>
    );
  return (
    <Steps>
      {keyStep}
      <StepItem n={2} title="Add to .cursor/mcp.json">
        <CodeBlock
          code={shown.cursor}
          copyText={s.cursor}
          label="mcp.json"
          copyLabel="Copy configuration"
        />
      </StepItem>
    </Steps>
  );
}

// a key for one client: a name and the days until it ends, on one line.
// the key itself shows once in the step, then only its start in the table
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
  const nameId = useId();
  const daysId = useId();
  const m = useMutation({
    mutationFn: create,
    onSuccess: (k) => {
      setForm({ name: "", days: String(MCP_KEY_DEFAULT_DAYS) });
      onCreated(k);
    },
    onError: (err) => setErrors(serverErrors(err)),
  });
  const taken = nameTaken(takenNames, form.name);
  const problem = errors.name ?? errors.days ?? (taken ? TAKEN : null);
  return (
    <form
      noValidate
      aria-label="Create key"
      onSubmit={(e) => {
        e.preventDefault();
        const v = validateMcpKey(form);
        setErrors(v.errors);
        if (v.body) m.mutate(v.body);
      }}
      className="flex flex-col gap-2"
    >
      <div className="flex flex-wrap gap-2">
        <label htmlFor={nameId} className="sr-only">
          Name
        </label>
        <Input
          id={nameId}
          placeholder="Name, e.g. laptop"
          maxLength={100}
          autoComplete="off"
          value={form.name}
          aria-invalid={errors.name ? true : undefined}
          onChange={(e) => setForm({ ...form, name: e.target.value })}
          className="min-w-48 flex-1 max-lg:h-(--vs-control-touch)"
        />
        <span className="flex h-(--vs-control) items-center gap-2 rounded-lg border border-line-input bg-sunken px-3 text-small text-fg-2 max-lg:h-(--vs-control-touch)">
          <label htmlFor={daysId}>Ends in</label>
          <input
            id={daysId}
            type="number"
            inputMode="numeric"
            min={1}
            max={MCP_KEY_MAX_DAYS}
            value={form.days}
            aria-invalid={errors.days ? true : undefined}
            onChange={(e) => setForm({ ...form, days: e.target.value })}
            className="w-12 rounded-sm bg-transparent text-fg tabular outline-none focus-ring"
          />
          <span>days</span>
        </span>
        <Button type="submit" pending={m.isPending} className="max-lg:h-(--vs-control-touch)">
          {m.isPending ? "Creating" : "Create key"}
        </Button>
      </div>
      {problem ? (
        <p
          role={errors.name || errors.days ? "alert" : undefined}
          className={cn(
            "text-small",
            errors.name || errors.days ? "text-critical" : "text-warning",
          )}
        >
          {problem}
        </p>
      ) : null}
      <FormMessage
        error={m.error && Object.keys(serverErrors(m.error)).length === 0 ? m.error : null}
      />
    </form>
  );
}

const TAKEN = "A key with this name exists. Use another name to tell them apart.";

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
      className: "w-[34%]",
      cell: (c) => (
        <span className="flex min-w-0 flex-col gap-0.5 py-1">
          <span className="flex min-w-0 flex-wrap items-baseline gap-x-2.5">
            <span className="font-medium break-words text-fg">{c.name}</span>
            {c.prefix ? (
              <code className="font-mono text-caption text-fg-3">{c.prefix}...</code>
            ) : null}
          </span>
          {c.display_name ? (
            <span className="truncate text-caption font-normal text-fg-2">
              acts as {c.display_name}
            </span>
          ) : null}
        </span>
      ),
    },
    {
      key: "kind",
      header: "Kind",
      cell: (c) => <span className="text-fg-2">{c.kind === "key" ? "Key" : "Connector"}</span>,
    },
    {
      key: "status",
      header: "Status",
      mobile: true,
      cell: (c) => <StatusBadge kind="key" status={c.status} />,
    },
    {
      key: "ends",
      header: "Ends",
      minWidth: "min-w-36",
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
          <span className="text-fg-2">{NOT_USED}</span>
        ),
    },
  ];
  return (
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
  );
}

// one wording for a credential that never sent a request, on this page and
// on Connect API
export const NOT_USED = "Never";

// when a credential stops: revoked with the reason, expired, expires, or
// kept alive by its client
export function credentialEnd(c: McpCredential): string {
  if (c.revoked_at)
    return `Revoked ${formatDateTimeUtc(c.revoked_at)}${
      c.revoked_reason ? `: ${REVOKED_REASON[c.revoked_reason] ?? c.revoked_reason}` : ""
    }`;
  // the status column says active or expired; the cell holds the date only
  if (c.expires_at) return formatDate(c.expires_at);
  return "Renewed by the client";
}

// numbered steps of one client
function Steps({ children }: { children: ReactNode }) {
  return <ol className="flex min-w-0 list-none flex-col gap-5">{children}</ol>;
}

function StepItem({ n, title, children }: { n: number; title: string; children?: ReactNode }) {
  return (
    <li className="grid min-w-0 grid-cols-[26px_minmax(0,1fr)] gap-x-3.5">
      <span
        aria-hidden
        className="flex size-[26px] items-center justify-center rounded-full border border-line-strong text-caption text-fg-2 tabular"
      >
        {n}
      </span>
      <div className="flex min-w-0 flex-col gap-2.5 pt-0.5">
        <span className="text-body font-medium text-fg">{title}</span>
        {children}
      </div>
    </li>
  );
}

// two live credentials with one name can only be told apart by their
// prefix, so the form warns before it creates a second one
export function nameTaken(names: string[], name: string): boolean {
  const n = name.trim().toLowerCase();
  return n !== "" && names.some((x) => x.trim().toLowerCase() === n);
}
