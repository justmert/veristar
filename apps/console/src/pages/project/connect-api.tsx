import { useMutation, useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { fade } from "@veristar/brand/motion";
import { ArrowRight, Ban, Check, ChevronRight, ExternalLink, KeyRound } from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
import { type FormEvent, type ReactNode, useId, useState } from "react";
import { Link } from "react-router";
import { ApiError } from "@/api/client";
import { api } from "@/api/endpoints";
import { keys, otlpTokensQuery } from "@/api/queries";
import type { OtlpToken, OtlpTokenCreated, Tenant } from "@/api/schemas";
import { CodeBlock, CopyField } from "@/components/code-block";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { type Column, DataTable } from "@/components/data-table";
import { FormMessage } from "@/components/error-panel";
import { TextField } from "@/components/form-field";
import { Notice } from "@/components/notice";
import { PageHeader } from "@/components/page-header";
import { SecretPanel } from "@/components/secret-panel";
import { Missing } from "@/components/states";
import { StatusDot } from "@/components/status";
import { StatusBadge } from "@/components/status-badge";
import { toast } from "@/components/toast";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { formatDate, formatDateTimeUtc } from "@/lib/format";
import { siblingUrl } from "@/lib/hosts";
import { inline } from "@/lib/markdown";
import { type OtelGuide, otelGuides, otlpEndpoint } from "@/lib/otel-guides";
import * as v from "@/lib/validate";
import { useProject } from "@/pages/project-layout";
import { queryClient } from "@/query-client";
import { PAGE_HEADERS } from "./page-frame";

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
  const [showRevoked, setShowRevoked] = useState(false);
  const [created, setCreated] = useState<OtlpTokenCreated | null>(null);
  const list = useQuery({
    ...otlpTokensQuery(tenant.id, showRevoked),
    refetchInterval: (q) => (waiting(q.state.data) ? FIRST_DATA_POLL : false),
  });
  const tokens = list.data ?? [];
  const hasToken = tokens.some((t) => t.status === "active") || created !== null;
  const lastUsed = lastUsedToken(tokens);

  return (
    <div className="flex flex-col gap-8">
      <PageHeader {...PAGE_HEADERS["connect-api"]} />

      <div className="grid items-start gap-x-10 gap-y-8 xl:grid-cols-[minmax(0,1fr)_18rem]">
        <div className="flex min-w-0 flex-col gap-8">
          <ol
            aria-label="Setup steps"
            className="flex min-w-0 flex-col rounded-lg border border-line bg-raised"
          >
            <Step n={1} state={hasToken ? "done" : "current"} title="Create an ingest token">
              <p className="max-w-prose text-small text-fg-2">
                Use one token for each app or environment. Then you can revoke one token and keep
                the others.
              </p>
              <AnimatePresence initial={false}>
                {created ? (
                  <motion.div key={created.id} {...fade}>
                    <CreatedToken token={created} onDone={() => setCreated(null)} />
                  </motion.div>
                ) : null}
              </AnimatePresence>
              <CreateGate tenant={tenant} canCreate={termsCurrent} onCreated={setCreated} />
            </Step>
            <Step
              n={2}
              state={hasToken ? (lastUsed ? "done" : "current") : "next"}
              title="Add a second exporter"
            >
              <Exporter endpoint={endpoint} />
            </Step>
            <Step n={3} state={lastUsed ? "done" : "next"} title="First data" last>
              <FirstData
                tenantId={tenant.id}
                hasToken={hasToken}
                lastUsed={lastUsed}
                loading={list.isPending}
              />
            </Step>
          </ol>

          <TokensSection
            tenantId={tenant.id}
            list={list}
            showRevoked={showRevoked}
            onShowRevoked={setShowRevoked}
          />
        </div>
        <KeptSection />
      </div>
    </div>
  );
}

// a numbered step of the setup: done (a check), the current one (accent
// ring) or a later one (muted)
function Step({
  n,
  state,
  title,
  last = false,
  children,
}: {
  n: number;
  state: "done" | "current" | "next";
  title: string;
  last?: boolean;
  children: ReactNode;
}) {
  return (
    <li
      className={cn(
        "grid min-w-0 grid-cols-[1.75rem_minmax(0,1fr)] gap-3 p-5 max-sm:p-4",
        !last && "border-b border-line",
      )}
    >
      <span
        className={cn(
          "flex size-7 items-center justify-center rounded-full font-mono text-small transition-colors duration-(--vs-duration-change) ease-standard",
          state === "done"
            ? "bg-success text-ground"
            : state === "current"
              ? "border-2 border-accent-text text-accent-text"
              : "border border-line-strong text-fg-2",
        )}
      >
        {state === "done" ? <Check aria-hidden className="size-4" strokeWidth={3} /> : n}
        <span className="sr-only">
          {state === "done" ? ", done" : state === "current" ? ", to do now" : ""}
        </span>
      </span>
      <div className="flex min-w-0 flex-col gap-3">
        {/* the size stays outside cn: its merge treats a text size and a text
            colour as one group and keeps only the last */}
        <h2 className={`pt-1 text-title ${state === "next" ? "text-fg-2" : "text-fg"}`}>{title}</h2>
        {children}
      </div>
    </li>
  );
}

// a short numbered instruction inside a step
function SubStep({ n, title, children }: { n: string; title: string; children: ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <h3 className="flex items-baseline gap-2 text-body font-medium text-fg">
        <span className="font-mono text-small text-fg-2">{n}</span>
        {title}
      </h3>
      <div className="flex min-w-0 flex-col gap-2">{children}</div>
    </div>
  );
}

function Exporter({ endpoint }: { endpoint: string | null }) {
  const guides = otelGuides();
  return (
    <div className="flex min-w-0 flex-col gap-6">
      <SubStep n="a" title="Copy the endpoint">
        {endpoint ? (
          <CopyField value={endpoint} label="endpoint" className="max-w-md" />
        ) : (
          <p className="max-w-prose text-small text-fg-2">
            The endpoint is the console's host name with otlp. in place of app.
          </p>
        )}
        <p className="max-w-prose text-small text-fg-2">
          OTLP over HTTP or gRPC, port 443. Send the token as{" "}
          <code className="font-mono text-fg">Authorization: Bearer sa_otlp_...</code> on this
          exporter only.
        </p>
      </SubStep>
      <SubStep n="b" title="Add the exporter to your app">
        <p className="max-w-prose text-small text-fg-2">
          Keep the exporter to your own backend. Add a second one with the token. Set service.name,
          so each service shows separately.
        </p>
        <Tabs defaultValue={guides[0]?.id ?? "node"} className="min-w-0 gap-3">
          <TabsList aria-label="Language" className="max-w-full overflow-x-auto">
            {guides.map((g) => (
              <TabsTrigger key={g.id} value={g.id} className="max-lg:h-(--vs-control-touch)">
                {g.label}
              </TabsTrigger>
            ))}
          </TabsList>
          {guides.map((g) => (
            <TabsContent key={g.id} value={g.id} className="min-w-0 animate-rise-in">
              <Guide guide={g} />
            </TabsContent>
          ))}
        </Tabs>
      </SubStep>
    </div>
  );
}

// a closed section of extra detail
function More({ title, children }: { title: string; children: ReactNode }) {
  return (
    <details className="group min-w-0 rounded-lg border border-line text-small">
      <summary className="flex min-h-10 cursor-pointer list-none items-center gap-2 rounded-lg px-3 text-fg-2 transition-colors duration-(--vs-duration-press) ease-standard hover:text-fg focus-ring max-lg:min-h-(--vs-control-touch) [&::-webkit-details-marker]:hidden">
        <ChevronRight
          aria-hidden
          className="size-4 transition-transform duration-(--vs-duration-press) ease-standard group-open:rotate-90"
        />
        {title}
      </summary>
      <div className="flex min-w-0 flex-col gap-3 border-t border-line p-3">{children}</div>
    </details>
  );
}

function Guide({ guide }: { guide: OtelGuide }) {
  return (
    <div className="flex min-w-0 flex-col gap-4">
      {guide.files.map((f) => (
        <div key={`${f.path}:${f.caption}`} className="flex min-w-0 flex-col gap-2">
          <p className="text-small text-fg-2">{f.caption}</p>
          <CodeBlock
            code={f.code}
            className="min-w-0 [&_pre]:max-h-96"
            label={<code className="font-mono text-fg">{f.path}</code>}
            copyLabel={`Copy ${f.path}`}
          />
        </div>
      ))}
      <More title={`Environment variables (${guide.env.length})`}>
        <div className="min-w-0 overflow-x-auto rounded-md border border-line">
          <table className="w-full min-w-120 text-left text-small">
            <thead className="text-caption text-fg-2">
              <tr className="border-b border-line">
                <th scope="col" className="h-9 px-3 font-medium">
                  Variable
                </th>
                <th scope="col" className="px-3 font-medium">
                  Values
                </th>
                <th scope="col" className="px-3 font-medium">
                  Purpose
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line-subtle text-fg-2">
              {guide.env.map((r) => (
                <tr key={r.name}>
                  <td className="px-3 py-2 font-mono text-caption whitespace-nowrap text-fg">
                    {r.name}
                  </td>
                  <td className="px-3 py-2">{inline(r.values)}</td>
                  <td className="px-3 py-2">{inline(r.purpose)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <p className="max-w-prose text-fg-2">{inline(guide.envNote)}</p>
      </More>
      <More title="About this example">
        {guide.prose.map((b) =>
          b.kind === "p" ? (
            <p key={b.text} className="max-w-prose text-fg-2">
              {inline(b.text)}
            </p>
          ) : (
            <CodeBlock key={b.text} code={b.text} className="min-w-0" />
          ),
        )}
        <p className="text-fg-2">
          The full example is in <code className="font-mono text-fg">{guide.dir}</code> of the
          repository.
        </p>
      </More>
    </div>
  );
}

function FirstData({
  tenantId,
  hasToken,
  lastUsed,
  loading,
}: {
  tenantId: string;
  hasToken: boolean;
  lastUsed: OtlpToken | null;
  loading: boolean;
}) {
  const state = loading ? "loading" : lastUsed ? "received" : hasToken ? "waiting" : "none";
  return (
    <div aria-live="polite">
      <AnimatePresence mode="wait" initial={false}>
        <motion.div key={state} {...fade} className="flex flex-col gap-2">
          {state === "received" && lastUsed ? (
            <div
              data-arrived=""
              className="flex animate-highlight flex-col gap-3 rounded-lg border border-success-line p-4"
            >
              <p className="flex items-center gap-2 text-body font-medium text-fg">
                <Check aria-hidden className="size-4 text-success" strokeWidth={2.6} />
                Data arrived
              </p>
              <p className="text-small text-fg-2">
                {lastUsed.name} was last used {formatDateTimeUtc(lastUsed.last_seen_at ?? "")}.
              </p>
              <Button asChild variant="outline" size="sm" className="self-start">
                <Link to={`/projects/${tenantId}/api`}>
                  See the traffic
                  <ArrowRight aria-hidden />
                </Link>
              </Button>
            </div>
          ) : state === "waiting" ? (
            <>
              <p className="flex items-center gap-2 text-body text-fg">
                <StatusDot tone="info" live />
                Waiting for the first data
              </p>
              <p className="max-w-prose text-small text-fg-2">
                This page checks every 30 seconds. Exporters send in batches, so the first data can
                take a minute or two.
              </p>
            </>
          ) : state === "none" ? (
            <p className="text-small text-fg-2">This step completes when an app sends data.</p>
          ) : null}
        </motion.div>
      </AnimatePresence>
    </div>
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
  const [revoking, setRevoking] = useState<OtlpToken | null>(null);
  const revoke = useMutation({
    mutationFn: (t: OtlpToken) => api.revokeOtlpToken(tenantId, t.id),
    onSuccess: async (_, t) => {
      setRevoking(null);
      toast.success(`${t.name} is revoked.`);
      await queryClient.invalidateQueries({ queryKey: keys.otlpTokensAll(tenantId) });
    },
  });
  const columns: Column<OtlpToken>[] = [
    {
      key: "name",
      header: "Name",
      primary: true,
      minWidth: "min-w-48",
      // a long name truncates, so the dates and Revoke stay in view
      className: "max-w-64",
      truncate: (t) => t.name,
      cell: (t) => (
        <span className="flex min-w-0 items-center gap-2">
          <span className="truncate font-medium text-fg">{t.name}</span>
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
      cell: (t) => <span className="whitespace-nowrap">{formatDate(t.created_at)}</span>,
    },
    {
      key: "used",
      header: "Last used",
      mobile: true,
      cell: (t) =>
        t.last_seen_at ? (
          <span className="whitespace-nowrap">{formatDateTimeUtc(t.last_seen_at)}</span>
        ) : (
          <Missing reason="The token was not used yet" />
        ),
    },
    {
      key: "action",
      mobile: true,
      header: "Actions",
      align: "right",
      cell: (t) =>
        t.status === "active" ? (
          <Button
            variant="destructive-outline"
            size="sm"
            className="relative z-(--vs-layer-raised) max-lg:h-(--vs-control-touch)"
            onClick={() => {
              revoke.reset();
              setRevoking(t);
            }}
          >
            <Ban aria-hidden />
            Revoke
          </Button>
        ) : null,
    },
  ];

  return (
    <section aria-labelledby="tokens-h" className="flex min-w-0 flex-col gap-4">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div className="flex flex-col gap-1">
          <h2 id="tokens-h" className="text-title text-fg">
            Tokens
          </h2>
          <p className="text-small text-fg-2">The full token shows once, when you create it.</p>
        </div>
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
      </div>
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
        empty={{
          title: showRevoked ? "No tokens yet." : "No active tokens.",
          text: "Create a token in step 1.",
        }}
      />
      <ConfirmDialog
        open={revoking !== null}
        onOpenChange={(o) => {
          if (!o) setRevoking(null);
        }}
        title={`Revoke ${revoking?.name ?? "the token"}?`}
        description="The collector refuses this token within seconds. Apps that still send with it get 401. You cannot undo this."
        confirmLabel="Revoke token"
        pendingLabel="Revoking"
        pending={revoke.isPending}
        error={revoke.error}
        onConfirm={() => revoking && revoke.mutate(revoking)}
      />
    </section>
  );
}

// the create form only where the control plane would accept it (contract
// 6.6); otherwise the reason, so nobody fills in a form that must fail
function CreateGate({
  tenant,
  canCreate,
  onCreated,
}: {
  tenant: Tenant;
  canCreate: boolean;
  onCreated: (t: OtlpTokenCreated) => void;
}) {
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
  return <CreateTokenForm tenantId={tenant.id} onCreated={onCreated} />;
}

export function CreateTokenForm({
  tenantId,
  onCreated,
}: {
  tenantId: string;
  onCreated: (t: OtlpTokenCreated) => void;
}) {
  const [values, setValues] = useState<TokenValues>({ name: "", environment: "" });
  const [errors, setErrors] = useState<TokenErrors>({});
  const create = useMutation({
    mutationFn: (body: { name: string; environment?: string }) =>
      api.createOtlpToken(tenantId, body),
    onSuccess: async (t) => {
      setValues({ name: "", environment: "" });
      onCreated(t);
      toast.success(`${t.name} is created. Copy the token now.`);
      await queryClient.invalidateQueries({ queryKey: keys.otlpTokensAll(tenantId) });
    },
    onError: (err) => setErrors(serverTokenErrors(err)),
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    const found = validateToken(values);
    setErrors(found);
    if (Object.keys(found).length > 0) return;
    const env = values.environment.trim();
    create.mutate({ name: values.name.trim(), ...(env ? { environment: env } : {}) });
  }

  return (
    <form noValidate onSubmit={submit} aria-label="Create a token" className="flex flex-col gap-3">
      <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_12rem_auto] sm:items-start">
        <TextField
          label="Token name"
          name="name"
          autoComplete="off"
          maxLength={100}
          placeholder="Payments API"
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
        <Button
          type="submit"
          className="max-lg:h-(--vs-control-touch) sm:mt-6"
          pending={create.isPending}
        >
          {create.isPending ? null : <KeyRound aria-hidden />}
          {create.isPending ? "Creating" : "Create token"}
        </Button>
      </div>
      <FormMessage
        error={
          create.error && Object.keys(serverTokenErrors(create.error)).length === 0
            ? create.error
            : null
        }
      />
    </form>
  );
}

// the plaintext lives only in this component's props, never in the query
// cache, and is gone once the panel is closed (contract 6.2: shown once)
export function CreatedToken({ token, onDone }: { token: OtlpTokenCreated; onDone: () => void }) {
  return (
    <SecretPanel
      value={token.token}
      title={`Copy the token ${token.name} now`}
      description="Store it as a secret of your app. It is shown only this once. If you lose it, create a new token and revoke this one."
      onDone={onDone}
    />
  );
}

// what the service keeps of the data, in a few lines; the full list is in
// the docs
function KeptSection() {
  const docs = siblingUrl(window.location.hostname, "docs");
  return (
    <aside aria-labelledby="otlp-kept" className="flex flex-col gap-3 xl:sticky xl:top-6">
      <h2 id="otlp-kept" className="text-caption text-fg-2">
        What the service keeps
      </h2>
      <ul className="flex flex-col gap-2 text-small text-fg-2">
        <li>
          Only HTTP server request duration is kept, with method, route, status code and service
          name.
        </li>
        <li>
          Other metrics, traces and logs are dropped. Ids and keys in routes become placeholders.
        </li>
        <li className="text-fg">Your own backend keeps the full data.</li>
      </ul>
      {docs ? (
        <a
          href={`${docs}/onboarding/api/what-gets-dropped`}
          className="inline-flex w-fit items-center gap-1 rounded-sm text-small text-fg underline decoration-line-strong underline-offset-4 transition-colors duration-(--vs-duration-press) ease-standard hover:decoration-fg focus-ring"
        >
          What the service drops
          <ExternalLink aria-hidden className="size-4" />
        </a>
      ) : null}
    </aside>
  );
}
