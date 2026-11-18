import { useMutation, useQuery } from "@tanstack/react-query";
import { CircleAlert, Plug, ShieldCheck } from "lucide-react";
import { useState } from "react";
import { ApiError } from "@/api/client";
import { type ConsentRequest, mcpApi, mcpKeys } from "@/api/mcp";
import { meQuery } from "@/api/queries";
import { ChoiceCard } from "@/components/choice-card";
import { FormMessage } from "@/components/error-panel";
import { Notice } from "@/components/notice";
import { PublicFrame } from "@/components/public-frame";
import { ErrorState, LoadingState, MessagePage, Skeleton } from "@/components/states";
import { Button } from "@/components/ui/button";
import { errorMessage } from "@/lib/errors";
import { ProjectsButton } from "./not-found";

// the oauth consent screen of the mcp authorization flow (contract 12.3).
// /oauth/authorize checks the client and sends the browser here with a
// sealed request; the person signs in with github, picks the project (or
// the operator view) and approves, and the page then follows the redirect
// back to the ai client. nothing is granted without the approve click.

const STORE = "veristar.mcp.consent";

// errors /oauth/authorize can send before it trusts the client
const AUTHORIZE_ERRORS: Record<string, string> = {
  invalid_client:
    "The AI client is not registered here. Remove the connector in the client and add it again.",
  invalid_request: "The AI client sent an authorization request this server cannot accept.",
};

// the sealed request can be longer than a sign-in return path may be, so it
// moves to session storage and the address bar keeps the short path; after
// the github sign-in the page finds it there
function takeRequest(): string | null {
  const params = new URLSearchParams(window.location.search);
  const fromUrl = params.get("request");
  try {
    if (fromUrl) {
      sessionStorage.setItem(STORE, fromUrl);
      window.history.replaceState(null, "", "/ai/consent");
      return fromUrl;
    }
    return sessionStorage.getItem(STORE);
  } catch {
    // storage can be blocked; the request from the address bar still works
    // for a person who is already signed in
    return fromUrl;
  }
}

function forget() {
  try {
    sessionStorage.removeItem(STORE);
  } catch {
    // nothing stored
  }
}

export function AiConsentPage() {
  const [params] = useState(() => new URLSearchParams(window.location.search));
  const error = params.get("error");
  const [req] = useState(() => (error ? null : takeRequest()));

  return (
    // the same frame and place as every other full-page message; only the
    // consent form itself is narrower
    <PublicFrame>
      {error ? (
        <Stop tone="critical" title="The connection was not set up">
          <p role="alert">
            {AUTHORIZE_ERRORS[error] ??
              params.get("error_description") ??
              "The AI client sent a request this server cannot accept. Start again from the AI client."}
          </p>
        </Stop>
      ) : req ? (
        <Signed request={req} />
      ) : (
        <Stop tone="info" title="No connection request">
          <p>
            Start from your AI client. Add the Veristar server URL as a connector, and the client
            opens this page.
          </p>
        </Stop>
      )}
    </PublicFrame>
  );
}

// a page that explains why there is nothing to approve, with the way back
function Stop({
  tone,
  title,
  children,
}: {
  tone: "info" | "critical";
  title: string;
  children?: React.ReactNode;
}) {
  return (
    <MessagePage
      icon={tone === "critical" ? <CircleAlert aria-hidden /> : <Plug aria-hidden />}
      tone={tone === "critical" ? "critical" : "neutral"}
      title={title}
      actions={<ProjectsButton />}
      className="w-full"
    >
      {children}
    </MessagePage>
  );
}

// rendered for a request; /api/me answers 401 without a session, and the
// query client then sends the browser to sign-in and back to /ai/consent
function Signed({ request }: { request: string }) {
  const me = useQuery(meQuery);
  const inspect = useQuery({
    queryKey: mcpKeys.consent(request),
    queryFn: () => mcpApi.inspect(request),
    enabled: me.isSuccess,
    staleTime: Number.POSITIVE_INFINITY,
    refetchOnWindowFocus: false,
  });
  if (me.isPending || (me.isSuccess && inspect.isPending))
    return (
      <LoadingState label="Loading the request" className="w-full max-w-dialog-sm">
        <Skeleton.Table rows={3} columns={1} />
      </LoadingState>
    );
  if (me.error)
    return (
      <ErrorState
        title="Your account did not load"
        error={me.error}
        onRetry={() => me.refetch()}
        className="w-full max-w-prose"
      />
    );
  if (inspect.error)
    return (
      <Stop tone="critical" title="This request cannot be approved">
        <p role="alert">{inspectErrorText(inspect.error)}</p>
        <p>A request expires after 10 minutes. Start again from the AI client.</p>
      </Stop>
    );
  if (!inspect.data) return null;
  return (
    <div className="w-full max-w-dialog-sm">
      <Consent request={request} info={inspect.data} login={me.data?.user.display_name ?? ""} />
    </div>
  );
}

// an expired or changed sealed request is a validation error of the whole
// request, not of a field: say what it means on this page
function inspectErrorText(err: unknown): string {
  if (err instanceof ApiError && err.code === "validation_failed")
    return "The request expired or is not complete.";
  return errorMessage(err);
}

function Consent({
  request,
  info,
  login,
}: {
  request: string;
  info: ConsentRequest;
  login: string;
}) {
  const first = info.tenants[0]?.id ?? (info.operator ? "operator" : "");
  const [choice, setChoice] = useState<string>(first);
  const go = (to: string) => {
    forget();
    window.location.assign(to);
  };
  const approve = useMutation({
    mutationFn: () =>
      choice === "operator"
        ? mcpApi.approve(request, "operator", null)
        : mcpApi.approve(request, "tenant", choice),
    onSuccess: (r) => go(r.redirect_to),
  });
  const deny = useMutation({
    mutationFn: () => mcpApi.deny(request),
    onSuccess: (r) => go(r.redirect_to),
  });
  const busy = approve.isPending || deny.isPending || approve.isSuccess || deny.isSuccess;
  const nothing = info.tenants.length === 0 && !info.operator;
  const operator = choice === "operator";

  return (
    <form
      aria-label="Approve the connection"
      className="flex flex-col rounded-lg border border-line bg-raised"
      onSubmit={(e) => {
        e.preventDefault();
        if (choice) approve.mutate();
      }}
    >
      <div className="flex flex-col gap-2 border-b border-line px-5 pt-6 pb-5 sm:px-6">
        <h1 className="text-heading text-fg">Connect {info.client_name} to Veristar</h1>
        <p className="text-body text-fg-2">
          Signed in as <span className="font-medium text-fg">{login}</span>.{" "}
          <span className="font-medium text-fg">{info.client_name}</span> asks to read analytics as
          you.
        </p>
        <div className="flex min-w-0 flex-col gap-1">
          <span className="text-caption text-fg-2">After approval, your browser goes back to</span>
          <code className="w-fit max-w-full rounded-sm border border-line bg-sunken px-2 py-1 font-mono text-small break-all text-fg">
            {info.redirect_origin}
          </code>
        </div>
      </div>
      <ul className="flex flex-col gap-2 border-b border-line px-5 py-4 text-small sm:px-6">
        <li className="flex gap-2">
          <ShieldCheck aria-hidden className="mt-1 size-4 shrink-0 text-success" />
          <span>
            <span className="font-medium text-fg">Read-only.</span>{" "}
            <span className="text-fg-2">
              Web page views, events and visits, and API request counts, errors and latency. The
              client cannot change anything.
            </span>
          </span>
        </li>
        <li className="flex gap-2" data-scope={operator ? "operator" : "tenant"}>
          <ShieldCheck aria-hidden className="mt-1 size-4 shrink-0 text-success" />
          {operator ? (
            <span>
              <span className="font-medium text-fg">Every project, as daily totals.</span>{" "}
              <span className="text-fg-2">
                The operator view. It ends when you stop being an operator or you revoke the key.
              </span>
            </span>
          ) : (
            <span>
              <span className="font-medium text-fg">One project only, the one you choose.</span>{" "}
              <span className="text-fg-2">
                It ends when you leave the project, the project is suspended or removed, or you
                revoke it on the AI assistants page of the project.
              </span>
            </span>
          )}
        </li>
      </ul>
      <div className="flex flex-col gap-3 px-5 py-4 sm:px-6">
        {nothing ? (
          <Notice tone="warning" role="alert" title="Nothing to connect">
            You are not a member of a project that can be connected. Join or enroll a project first.
          </Notice>
        ) : (
          <fieldset className="flex flex-col gap-2">
            <legend className="mb-2 text-small font-medium text-fg">Give access to</legend>
            {info.tenants.map((t) => (
              <ChoiceCard
                key={t.id}
                name="scope"
                value={t.id}
                checked={choice === t.id}
                onChange={() => setChoice(t.id)}
                title={t.name}
                description={`${t.slug}, ${t.role}${t.status === "pending" ? ", in review" : ""}`}
              />
            ))}
            {info.operator ? (
              <ChoiceCard
                name="scope"
                value="operator"
                checked={choice === "operator"}
                onChange={() => setChoice("operator")}
                title="The operator view of every project"
                description="Daily totals, search and compare"
              />
            ) : null}
          </fieldset>
        )}
        <FormMessage error={approve.error} />
        <FormMessage error={deny.error} />
      </div>
      {/* the order and alignment of every dialog: the way back first, the
          action last, on the right */}
      <div className="flex flex-wrap justify-end gap-2 border-t border-line px-5 py-4 sm:px-6">
        <Button
          type="button"
          variant="outline"
          disabled={busy}
          pending={deny.isPending || deny.isSuccess}
          className="max-sm:h-(--vs-control-touch) max-sm:flex-1"
          onClick={() => deny.mutate()}
        >
          Deny
        </Button>
        <Button
          type="submit"
          disabled={busy || !choice}
          pending={approve.isPending || approve.isSuccess}
          className="max-sm:h-(--vs-control-touch) max-sm:flex-1"
        >
          {approve.isPending || approve.isSuccess ? "Approving" : "Approve"}
        </Button>
      </div>
    </form>
  );
}
