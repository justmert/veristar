import { useMutation, useQuery } from "@tanstack/react-query";
import { ArrowRight, Ban, ExternalLink, KeyRound } from "lucide-react";
import { type ReactNode, useId, useState } from "react";
import { Link } from "react-router";
import { ApiError } from "@/api/client";
import { api } from "@/api/endpoints";
import { keys, otlpTokensQuery } from "@/api/queries";
import type { OtlpToken, OtlpTokenCreated, Tenant } from "@/api/schemas";
import { CodeBlock } from "@/components/code-block";
import { CreateDialog } from "@/components/create-dialog";
import { type Column, DataTable } from "@/components/data-table";
import { TextField } from "@/components/form-field";
import { Notice } from "@/components/notice";
import { PageHeader } from "@/components/page-header";
import { Section } from "@/components/panel";
import { Missing } from "@/components/states";
import { StatusDot } from "@/components/status";
import { StatusBadge } from "@/components/status-badge";
import { toast } from "@/components/toast";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { formatDateTimeUtc } from "@/lib/format";
import { siblingUrl } from "@/lib/hosts";
import { otlpEndpoint } from "@/lib/otlp-endpoint";
import * as v from "@/lib/validate";
import { NOT_USED, nameTaken } from "@/pages/project/ai";
import { useProject } from "@/pages/project-layout";
import { queryClient } from "@/query-client";
import { ENDPOINT_PLACEHOLDER, exampleDocsPath, exporterSnippet, STACKS } from "./connect-snippets";
import { PAGE_HEADERS } from "./page-frame";
import { Step, Steps } from "./steps";

export type TokenValues = { name: string; environment: string };
export type TokenErrors = Partial<Record<keyof TokenValues, string>>;

// contract 6.2: environment is empty or a short lowercase label
const ENVIRONMENT = /^[a-z0-9]([a-z0-9._-]{0,31})?$/;

export function validateToken(values: TokenValues): TokenErrors {
  const out: TokenErrors = {};
  const name = v.siteName(values.name);
  if (name) out.name = v.fieldMessage(name);
  const env = values.environment.trim();
  if (env !== "" && !ENVIRONMENT.test(env)) {
    out.environment = "Use up to 32 lowercase letters, digits, dots, dashes or underscores.";
  }
  return out;
}

export function serverTokenErrors(err: unknown): TokenErrors {
  if (!(err instanceof ApiError) || err.code !== "validation_failed") return {};
  const out: TokenErrors = {};
  for (const [path, code] of Object.entries(err.fields)) {
    if (path === "environment") out.environment = v.fieldMessage(code);
    else if (code === "too_many")
      out.name = "This project has 50 active tokens. Revoke one before creating another.";
    else out.name = v.fieldMessage(code);
  }
  return out;
}

// the metrics writer stamps last_seen_at when data sent with a token is
// written (contract 6.1, at most once a minute), so the token list itself
// tells whether anything arrived yet. while nothing has, the list is asked
// again on this interval.
const FIRST_DATA_POLL = 30_000;

function waiting(tokens: OtlpToken[] | undefined): boolean {
  if (!tokens) return false;
  const active = tokens.filter((t) => t.status === "active");
  return active.length > 0 && active.every((t) => t.last_seen_at === null);
}

// the token that was used last, if any was
export function lastUsedToken(tokens: OtlpToken[]): OtlpToken | null {
  return (
    tokens
      .filter((t) => t.last_seen_at !== null)
      .sort((a, b) => Date.parse(b.last_seen_at ?? "") - Date.parse(a.last_seen_at ?? ""))[0] ??
    null
  );
}

export function ConnectApiPage() {
  const { tenant, termsCurrent } = useProject();
  const endpoint = otlpEndpoint(window.location.hostname);
  const docs = siblingUrl(window.location.hostname, "docs");
  const [showRevoked, setShowRevoked] = useState(false);
  const [creating, setCreating] = useState(false);
  const list = useQuery({
    ...otlpTokensQuery(tenant.id, showRevoked),
    refetchInterval: (q) => (waiting(q.state.data) ? FIRST_DATA_POLL : false),
  });
  const tokens = list.data ?? [];
  const active = tokens.filter((t) => t.status === "active");
  const hasToken = active.length > 0;
  const lastUsed = lastUsedToken(tokens);
  const blocked = createBlocked(tenant, termsCurrent);
  const create = (
    <Button onClick={() => setCreating(true)} className="w-fit">
      <KeyRound aria-hidden />
      Create token
    </Button>
  );

  return (
    <div className="flex flex-col gap-8">
      <PageHeader
        {...PAGE_HEADERS["connect-api"]}
        // the first token is created from the first step; later ones here
        primary={hasToken && !blocked ? create : null}
        secondary={
          docs ? (
            <Button asChild variant="ghost">
              <a href={`${docs}/onboarding/api/what-gets-dropped`}>
                What is kept
                <ExternalLink aria-hidden />
              </a>
            </Button>
          ) : null
        }
      />

      {hasToken && blocked ? blocked : null}
      <section
        aria-label="Setup"
        className="flex min-w-0 flex-col rounded-lg border border-line bg-raised p-5 max-sm:p-4"
      >
        <Steps label="Setup steps">
          <Step n={1} state={hasToken ? "done" : "current"} title="Create a token">
            {blocked ?? create}
          </Step>
          <Step
            n={2}
            state={hasToken ? (lastUsed ? "done" : "current") : "todo"}
            title="Add the exporter"
          >
            <Snippets endpoint={endpoint} docs={docs} />
          </Step>
          <Step
            n={3}
            state={lastUsed ? "done" : hasToken ? "waiting" : "todo"}
            title="Data arrived"
            aside={
              lastUsed ? (
                <Arrived tenantId={tenant.id} lastUsed={lastUsed} />
              ) : hasToken ? (
                <span
                  className="flex items-center gap-2 text-small text-fg-2"
                  aria-live="polite"
                  data-waiting=""
                >
                  <StatusDot tone="info" live />
                  Checking every 30 s
                </span>
              ) : null
            }
          />
        </Steps>
      </section>

      <TokensSection
        tenantId={tenant.id}
        list={list}
        showRevoked={showRevoked}
        onShowRevoked={setShowRevoked}
      />

      <CreateTokenDialog
        tenantId={tenant.id}
        open={creating}
        onOpenChange={setCreating}
        endpoint={endpoint}
        docs={docs}
        takenNames={active.map((t) => t.name)}
      />
    </div>
  );
}

// the exporter to add, per stack, with the endpoint and the token written
// in; the copied text holds the token, the shown one only its start
function Snippets({
  endpoint,
  docs,
  token,
}: {
  endpoint: string | null;
  docs: string | null;
  token?: string;
}) {
  const at = endpoint ?? ENDPOINT_PLACEHOLDER;
  const shown = token ? `${token.slice(0, 12)}...` : "<token>";
  return (
    <Tabs defaultValue={STACKS[0]?.id ?? "node"} className="min-w-0 gap-3">
      <TabsList aria-label="Language" className="max-w-full overflow-x-auto">
        {STACKS.map((g) => (
          <TabsTrigger key={g.id} value={g.id} className="max-lg:h-(--vs-control-touch)">
            {g.label}
          </TabsTrigger>
        ))}
      </TabsList>
      {STACKS.map((g) => (
        <TabsContent key={g.id} value={g.id} className="flex min-w-0 flex-col gap-2">
          <div data-snippet={g.id} className="min-w-0">
            <CodeBlock
              code={exporterSnippet(g.id, at, shown)}
              copyText={exporterSnippet(g.id, at, token ?? "<token>")}
              copyLabel={`Copy the ${g.label} exporter`}
            />
          </div>
          {docs ? (
            <a
              href={`${docs}${exampleDocsPath(g.id)}`}
              className="inline-flex w-fit items-center gap-1 rounded-sm text-small text-fg-2 transition-colors duration-(--vs-duration-press) ease-standard hover:text-fg focus-ring"
            >
              Full {g.label} example
              <ExternalLink aria-hidden className="size-3.5" />
            </a>
          ) : null}
        </TabsContent>
      ))}
    </Tabs>
  );
}

function Arrived({ tenantId, lastUsed }: { tenantId: string; lastUsed: OtlpToken }) {
  return (
    <span data-arrived="" className="flex flex-wrap items-center gap-x-3 gap-y-1 text-small">
      <span className="text-fg-2 tabular">{formatDateTimeUtc(lastUsed.last_seen_at ?? "")}</span>
      <Link
        to={`/projects/${tenantId}/api`}
        className="group inline-flex items-center gap-1 rounded-sm font-medium text-fg hover:underline focus-ring"
      >
        See the traffic
        <ArrowRight aria-hidden className="size-3.5" />
      </Link>
    </span>
  );
}

function TokensSection({
  tenantId,
  list,
  showRevoked,
  onShowRevoked,
}: {
  tenantId: string;
  list: ReturnType<typeof useQuery<OtlpToken[]>>;
  showRevoked: boolean;
  onShowRevoked: (on: boolean) => void;
}) {
  const revokedId = useId();
  const columns: Column<OtlpToken>[] = [
    {
      key: "name",
      header: "Name",
      primary: true,
      minWidth: "min-w-48",
      // the name takes the spare width and wraps rather than cuts
      className: "w-full",
      cell: (t) => (
        <span className="flex min-w-0 flex-wrap items-center gap-2">
          <span className="font-medium break-words text-fg">{t.name}</span>
          {t.environment ? (
            <span className="shrink-0 rounded-full border border-line-strong px-2 font-mono text-caption text-fg-2">
              {t.environment}
            </span>
          ) : null}
          {t.status === "active" ? null : <StatusBadge kind="key" status="revoked" />}
        </span>
      ),
    },
    {
      key: "prefix",
      header: "Token",
      mobile: true,
      cell: (t) => (
        <code className="font-mono text-small whitespace-nowrap text-fg">{t.prefix}...</code>
      ),
    },
    {
      key: "created",
      header: "Created",
      cell: (t) => <span className="whitespace-nowrap">{formatDateTimeUtc(t.created_at)}</span>,
    },
    {
      key: "used",
      header: "Last used",
      mobile: true,
      cell: (t) =>
        t.last_seen_at ? (
          <span className="whitespace-nowrap">{formatDateTimeUtc(t.last_seen_at)}</span>
        ) : (
          <Missing reason={NOT_USED} />
        ),
    },
  ];

  return (
    <Section
      title="Tokens"
      count={list.data?.length ?? null}
      actions={
        <span className="flex items-center gap-2">
          <Checkbox
            id={revokedId}
            checked={showRevoked}
            onCheckedChange={(c) => onShowRevoked(c === true)}
          />
          <label htmlFor={revokedId} className="text-small text-fg-2">
            Show revoked tokens
          </label>
        </span>
      }
    >
      {list.data && list.data.length === 0 ? (
        // nothing to list: one line, not an empty table
        <p className="text-small text-fg-2" data-tokens="none">
          {showRevoked ? "No tokens yet" : "No active tokens"}
        </p>
      ) : (
        <DataTable<OtlpToken>
          label="Tokens"
          columns={columns}
          rows={list.data ?? []}
          getRowKey={(t) => t.id}
          rowLabel={(t) => t.name}
          rowProps={(t) => ({ "data-token": t.id })}
          state={list.isPending ? "loading" : list.error ? "error" : "data"}
          error={list.error}
          onRetry={() => list.refetch()}
          loadingRows={2}
          empty={{ title: showRevoked ? "No tokens yet" : "No active tokens" }}
          rowActions={(t) => [
            t.status === "active" && {
              label: "Revoke",
              icon: <Ban aria-hidden />,
              destructive: true,
              confirm: {
                title: `Revoke the token “${t.name}”?`,
                description:
                  "The collector refuses this token within seconds. Apps that still send with it get 401. You cannot undo this.",
                confirmLabel: "Revoke token",
                pendingLabel: "Revoking",
                run: async () => {
                  await api.revokeOtlpToken(tenantId, t.id);
                  toast.success(`${t.name} is revoked.`);
                  await queryClient.invalidateQueries({ queryKey: keys.otlpTokensAll(tenantId) });
                },
              },
            },
          ]}
        />
      )}
    </Section>
  );
}

// the create action only where the control plane would accept it (contract
// 6.6); otherwise the reason, so nobody fills in a form that must fail
function createBlocked(tenant: Tenant, canCreate: boolean): ReactNode | null {
  if (tenant.status === "suspended")
    return (
      <Notice tone="info">
        New tokens cannot be created while the project is suspended. The collector refuses the
        existing tokens until an operator resumes the project.
      </Notice>
    );
  if (tenant.status !== "active")
    return (
      <Notice tone="info">You can create tokens once operators have approved the project.</Notice>
    );
  if (!canCreate)
    return (
      <Notice tone="warning">
        You can create tokens once an owner accepts the current terms. Revoking works now.
      </Notice>
    );
  return null;
}

// the form, then the token once with the exporter written for it, in one
// dialog. the plaintext lives only in this component's state and is gone
// once the dialog closes (contract 6.2: shown once)
function CreateTokenDialog({
  tenantId,
  open,
  onOpenChange,
  endpoint,
  docs,
  takenNames,
}: {
  tenantId: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  endpoint: string | null;
  docs: string | null;
  takenNames: string[];
}) {
  const [values, setValues] = useState<TokenValues>({ name: "", environment: "" });
  const [errors, setErrors] = useState<TokenErrors>({});
  const taken = nameTaken(takenNames, values.name);
  const create = useMutation({
    mutationFn: (body: { name: string; environment?: string }) =>
      api.createOtlpToken(tenantId, body),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: keys.otlpTokensAll(tenantId) });
    },
    onError: (err) => setErrors(serverTokenErrors(err)),
  });
  const created: OtlpTokenCreated | undefined = create.data;

  function submit() {
    const found = validateToken(values);
    setErrors(found);
    if (Object.keys(found).length > 0) return;
    const env = values.environment.trim();
    create.mutate({ name: values.name.trim(), ...(env ? { environment: env } : {}) });
  }

  return (
    <CreateDialog
      label="Create token"
      title="Create a token"
      trigger={false}
      open={open}
      onOpenChange={onOpenChange}
      pending={create.isPending}
      pendingLabel="Creating"
      error={
        create.error && Object.keys(serverTokenErrors(create.error)).length === 0
          ? create.error
          : null
      }
      onSubmit={submit}
      onClosed={() => {
        create.reset();
        setValues({ name: "", environment: "" });
        setErrors({});
      }}
      secret={
        created
          ? {
              value: created.token,
              noun: "token",
              name: created.name,
              children: <Snippets endpoint={endpoint} docs={docs} token={created.token} />,
            }
          : null
      }
    >
      <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_12rem]">
        <TextField
          label="Token name"
          name="name"
          autoComplete="off"
          maxLength={100}
          placeholder="Payments API"
          hint={
            taken ? (
              <span className="text-warning">
                A token with this name exists. Use another name to tell them apart.
              </span>
            ) : undefined
          }
          value={values.name}
          onChange={(e) => setValues((s) => ({ ...s, name: e.target.value }))}
          error={errors.name}
          className="max-lg:h-(--vs-control-touch)"
        />
        <TextField
          label="Environment"
          name="environment"
          optional
          autoCapitalize="none"
          autoComplete="off"
          spellCheck={false}
          maxLength={32}
          placeholder="production"
          value={values.environment}
          onChange={(e) => setValues((s) => ({ ...s, environment: e.target.value }))}
          error={errors.environment}
          className="max-lg:h-(--vs-control-touch)"
        />
      </div>
    </CreateDialog>
  );
}
