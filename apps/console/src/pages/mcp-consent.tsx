import { useMutation, useQuery } from "@tanstack/react-query";
import { ShieldCheck } from "lucide-react";
import { useState } from "react";
import { Link } from "react-router";
import { type ConsentRequest, mcpApi, mcpKeys } from "@/api/mcp";
import { meQuery } from "@/api/queries";
import { ErrorPanel, FormMessage, Loading } from "@/components/error-panel";
import { Mark } from "@/components/mark";
import { Button } from "@/components/ui/button";

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

export function McpConsentPage() {
  const [params] = useState(() => new URLSearchParams(window.location.search));
  const error = params.get("error");
  const [req] = useState(() => (error ? null : takeRequest()));

  return (
    <main className="grid min-h-dvh place-items-center bg-canvas px-4 py-10">
      <div className="w-full max-w-lg rounded-xl border bg-card p-6">
        <Mark className="size-7 text-primary" />
        {error ? (
          <>
            <h1 className="mt-4 text-xl font-semibold tracking-tight">
              The connection was not set up
            </h1>
            <p role="alert" className="mt-2 text-muted-foreground">
              {AUTHORIZE_ERRORS[error] ?? params.get("error_description") ?? error}
            </p>
            <BackLink />
          </>
        ) : req ? (
          <Signed request={req} />
        ) : (
          <>
            <h1 className="mt-4 text-xl font-semibold tracking-tight">No connection request</h1>
            <p className="mt-2 text-muted-foreground">
              Start from your AI client: add the Veristar server URL as a connector and it opens
              this page.
            </p>
            <BackLink />
          </>
        )}
      </div>
    </main>
  );
}

function BackLink() {
  return (
    <div className="mt-6">
      <Button asChild variant="outline">
        <Link to="/">Go to the console</Link>
      </Button>
    </div>
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
    return <Loading label="Loading the request" />;
  if (me.error) return <ErrorPanel error={me.error} onRetry={() => me.refetch()} />;
  if (inspect.error)
    return (
      <>
        <h1 className="mt-4 text-xl font-semibold tracking-tight">
          This request cannot be approved
        </h1>
        <FormMessage error={inspect.error} />
        <p className="mt-2 text-sm text-muted-foreground">
          Requests expire after 10 minutes. Start again from the AI client.
        </p>
        <BackLink />
      </>
    );
  if (!inspect.data) return null;
  return <Consent request={request} info={inspect.data} login={me.data?.user.github_login ?? ""} />;
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

  return (
    <form
      aria-label="Approve the connection"
      onSubmit={(e) => {
        e.preventDefault();
        if (choice) approve.mutate();
      }}
    >
      <h1 className="mt-4 text-xl font-semibold tracking-tight">
        Connect {info.client_name} to Veristar
      </h1>
      <p className="mt-2 text-sm text-muted-foreground">
        Signed in as <span className="font-medium text-foreground">{login}</span>.{" "}
        <span className="font-medium text-foreground">{info.client_name}</span> asks to read
        analytics as you. After approval your browser returns to{" "}
        <code className="font-mono text-xs">{info.redirect_origin}</code>.
      </p>
      <ul className="mt-4 space-y-1.5 text-sm">
        <li className="flex gap-2">
          <ShieldCheck aria-hidden className="mt-0.5 size-4 shrink-0 text-primary" />
          Read-only: web traffic, events and sessions as Umami holds them, and API request counts,
          errors and latency. Nothing can be changed.
        </li>
        <li className="flex gap-2">
          <ShieldCheck aria-hidden className="mt-0.5 size-4 shrink-0 text-primary" />
          One project only, the one you choose. It ends when you leave the project, the project is
          suspended or removed, or you revoke it on the project's AI page.
        </li>
      </ul>
      {info.tenants.length === 0 && !info.operator ? (
        <p role="alert" className="mt-4 text-sm text-destructive">
          You are not a member of a project that can be connected.
        </p>
      ) : (
        <fieldset className="mt-5 space-y-2 text-sm">
          <legend className="mb-1 font-medium">Give access to</legend>
          {info.tenants.map((t) => (
            <label key={t.id} className="flex items-center gap-2">
              <input
                type="radio"
                name="scope"
                className="size-4 accent-primary"
                checked={choice === t.id}
                onChange={() => setChoice(t.id)}
              />
              {t.name}
              <span className="text-xs text-muted-foreground">
                {t.slug}, {t.role}
                {t.status === "pending" ? ", in review" : ""}
              </span>
            </label>
          ))}
          {info.operator ? (
            <label className="flex items-center gap-2">
              <input
                type="radio"
                name="scope"
                className="size-4 accent-primary"
                checked={choice === "operator"}
                onChange={() => setChoice("operator")}
              />
              The operator view of every project
              <span className="text-xs text-muted-foreground">
                daily aggregates, search and compare
              </span>
            </label>
          ) : null}
        </fieldset>
      )}
      {approve.error ? <FormMessage error={approve.error} /> : null}
      {deny.error ? <FormMessage error={deny.error} /> : null}
      <div className="mt-6 flex flex-wrap gap-2">
        <Button type="submit" disabled={busy || !choice}>
          {approve.isPending || approve.isSuccess ? "Approving" : "Approve"}
        </Button>
        <Button type="button" variant="outline" disabled={busy} onClick={() => deny.mutate()}>
          Deny
        </Button>
      </div>
    </form>
  );
}
