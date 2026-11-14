import { CircleAlert, Clock, RotateCcw, SearchX } from "lucide-react";
import type { ReactNode } from "react";
import { Link, useSearchParams } from "react-router";
import { webDashboardUrl } from "@/api/endpoints";
import { PublicFrame } from "@/components/public-frame";
import { Button } from "@/components/ui/button";

type SsoError = { title: string; text: string; retry: boolean; icon: ReactNode; tone: string };

const WAIT = <Clock aria-hidden />;
const FAIL = <CircleAlert aria-hidden />;

// failure codes of the dashboard sign-in redirect (contract 4.4.1)
const SSO_ERRORS: Record<string, SsoError> = {
  not_found: {
    title: "This site is not in your projects",
    text: "That site does not exist or is not in one of your projects.",
    retry: false,
    icon: <SearchX aria-hidden />,
    tone: "text-fg-2",
  },
  cross_site: {
    title: "Open the dashboard from the console",
    text: "The dashboard link was opened from another site. Open it from the console instead.",
    retry: true,
    icon: FAIL,
    tone: "text-warning",
  },
  provisioning_pending: {
    title: "The dashboard is still being set up",
    text: "This takes a minute after a project or site is created.",
    retry: true,
    icon: WAIT,
    tone: "text-warning",
  },
  umami_unavailable: {
    title: "The web dashboard is unreachable",
    text: "The dashboard service did not answer. Try again in a moment.",
    retry: true,
    icon: FAIL,
    tone: "text-critical",
  },
  rate_limited: {
    title: "Too many dashboard sign-ins",
    text: "There were too many in the last minute. Wait a moment, then try again.",
    retry: true,
    icon: WAIT,
    tone: "text-warning",
  },
  internal: {
    title: "The web dashboard did not open",
    text: "Opening the dashboard failed on our side.",
    retry: true,
    icon: FAIL,
    tone: "text-critical",
  },
};

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

export function SsoErrorPage() {
  const [params] = useSearchParams();
  const raw = params.get("code") ?? "internal";
  const code = Object.hasOwn(SSO_ERRORS, raw) ? raw : "internal";
  const site = params.get("site");
  const info = SSO_ERRORS[code] as SsoError;
  const retrySite = info.retry && site && UUID.test(site) ? site : null;

  return (
    <PublicFrame>
      <div className="flex w-full max-w-dialog-sm flex-col gap-5">
        <span
          className={`flex size-10 items-center justify-center rounded-lg border border-line-strong [&_svg]:size-5 ${info.tone}`}
        >
          {info.icon}
        </span>
        <div className="flex flex-col gap-2">
          <h1 className="text-page tracking-tight text-fg">{info.title}</h1>
          <p role="alert" className="text-body text-fg-2">
            {info.text}
          </p>
        </div>
        <div className="flex flex-wrap gap-2.5">
          {retrySite ? (
            <Button asChild className="max-sm:h-(--vs-control-touch)">
              <a href={webDashboardUrl(retrySite, "primary")}>
                <RotateCcw aria-hidden />
                Try again
              </a>
            </Button>
          ) : null}
          <Button asChild variant="outline" className="max-sm:h-(--vs-control-touch)">
            <Link to="/">Back to your projects</Link>
          </Button>
        </div>
        <p className="text-caption font-normal text-fg-3">
          Reference <span className="font-mono">{code}</span>. The web dashboard runs on Umami.
        </p>
      </div>
    </PublicFrame>
  );
}
