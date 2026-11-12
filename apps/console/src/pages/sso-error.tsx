import { Link, useSearchParams } from "react-router";
import { webDashboardUrl } from "@/api/endpoints";
import { Mark } from "@/components/mark";
import { Button } from "@/components/ui/button";

// failure codes of the dashboard sign-in redirect (contract 4.4.1)
const SSO_ERRORS: Record<string, { text: string; retry: boolean }> = {
  not_found: { text: "That site does not exist or is not in one of your projects.", retry: false },
  cross_site: {
    text: "The dashboard link was opened from another site. Open it from the console instead.",
    retry: true,
  },
  provisioning_pending: {
    text: "The dashboard for this site is still being set up. This takes a minute after a project or site is created.",
    retry: true,
  },
  umami_unavailable: { text: "The web dashboard is unreachable right now.", retry: true },
  rate_limited: {
    text: "Too many dashboard sign-ins in the last minute. Wait a moment.",
    retry: true,
  },
  internal: { text: "Opening the dashboard failed on our side.", retry: true },
};

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

export function SsoErrorPage() {
  const [params] = useSearchParams();
  const code = params.get("code") ?? "internal";
  const site = params.get("site");
  const info = SSO_ERRORS[code] ?? (SSO_ERRORS.internal as { text: string; retry: boolean });
  const retrySite = info.retry && site && UUID.test(site) ? site : null;

  return (
    <main className="grid min-h-dvh place-items-center bg-canvas px-4">
      <div className="w-full max-w-md rounded-xl border bg-card p-6">
        <Mark className="size-7 text-primary" />
        <h1 className="mt-4 text-xl font-semibold tracking-tight">
          The web dashboard did not open
        </h1>
        <p role="alert" className="mt-2 text-muted-foreground">
          {info.text}
        </p>
        <div className="mt-6 flex flex-wrap gap-2">
          {retrySite ? (
            <Button asChild>
              <a href={webDashboardUrl(retrySite, "primary")}>Try again</a>
            </Button>
          ) : null}
          <Button asChild variant="outline">
            <Link to="/">Back to your projects</Link>
          </Button>
        </div>
      </div>
    </main>
  );
}
