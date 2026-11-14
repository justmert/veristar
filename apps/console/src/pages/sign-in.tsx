import { CircleAlert, CircleCheck, FileText, Shield } from "lucide-react";
import { Link, useSearchParams } from "react-router";
import { signInUrl } from "@/api/endpoints";
import { PublicFrame } from "@/components/public-frame";
import { Button } from "@/components/ui/button";

// failure codes of the oauth callback (contract 4.1.2)
const SIGN_IN_ERRORS: Record<string, string> = {
  state_mismatch:
    "The sign-in link expired or was opened in another browser. Start again from this page.",
  access_denied: "GitHub sign-in was cancelled. Nothing was shared.",
  github_error: "GitHub refused the sign-in request. Try again in a moment.",
  rate_limited: "Too many sign-ins right now. Wait a few seconds and start again.",
  exchange_failed: "GitHub did not confirm the sign-in. Start again from this page.",
  github_unavailable: "GitHub could not be reached. Try again in a moment.",
  internal: "Sign-in failed on our side. Try again; if it keeps failing, report it.",
};

// same rule as the control plane (contract 4.1.1): a console path, never a
// scheme-relative or backslash path that a browser would read as another host
export function safeReturnTo(v: string | null): string {
  if (!v || v.length > 512 || !v.startsWith("/") || v.startsWith("//") || v.startsWith("/\\"))
    return "/";
  return v;
}

export function SignInPage() {
  const [params] = useSearchParams();
  const code = params.get("error");
  const message = code ? (SIGN_IN_ERRORS[code] ?? SIGN_IN_ERRORS.internal) : null;
  const returnTo = safeReturnTo(params.get("return_to"));
  // set by the account page after DELETE /api/me (contract 4.1.6)
  const deleted = !message && params.get("deleted") === "1";

  return (
    <PublicFrame>
      <div className="flex w-full max-w-[24rem] flex-col gap-6">
        <div className="flex flex-col gap-2">
          <h1 className="text-h1 tracking-tight text-fg">Sign in</h1>
          <p className="text-body text-fg-2">
            Web and API analytics for projects building on Stellar, free for every project.
          </p>
        </div>

        {message ? (
          <p
            role="alert"
            className="flex animate-fade-in items-start gap-2.5 rounded-lg border border-critical-line bg-raised px-3.5 py-3 text-body text-fg"
          >
            <CircleAlert aria-hidden className="mt-0.5 size-4 shrink-0 text-critical" />
            {message}
          </p>
        ) : null}
        {deleted ? (
          <p
            role="status"
            className="flex animate-fade-in items-start gap-2.5 rounded-lg border border-line bg-raised px-3.5 py-3 text-body text-fg"
          >
            <CircleCheck aria-hidden className="mt-0.5 size-4 shrink-0 text-success" />
            Your account was deleted. Signing in again creates a new, empty account.
          </p>
        ) : null}

        <Button asChild size="lg" className="w-full">
          <a href={signInUrl(returnTo)}>
            <GitHubMark />
            Continue with GitHub
          </a>
        </Button>

        <ul className="flex flex-col gap-2 text-small text-fg-2">
          <li className="flex gap-2">
            <Shield aria-hidden className="mt-0.5 size-3.5 shrink-0 text-fg-3" />
            GitHub shares your public username and id. No email, no repository access.
          </li>
          <li className="flex gap-2">
            <FileText aria-hidden className="mt-0.5 size-3.5 shrink-0 text-fg-3" />
            <span>
              Enrolling a project means accepting the{" "}
              <Link
                to="/terms"
                className="rounded-sm text-fg underline decoration-line-strong underline-offset-2 transition-colors duration-(--vs-duration-press) hover:decoration-fg focus-ring"
              >
                terms
              </Link>
              .
            </span>
          </li>
        </ul>
      </div>
    </PublicFrame>
  );
}

// github's mark (octicons mark-github, mit licence); lucide no longer ships
// brand icons
function GitHubMark() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true" className="size-4.5" fill="currentColor">
      <path d="M8 0c4.42 0 8 3.58 8 8a8.013 8.013 0 0 1-5.45 7.59c-.4.08-.55-.17-.55-.38 0-.27.01-1.13.01-2.2 0-.75-.25-1.23-.54-1.48 1.78-.2 3.65-.88 3.65-3.95 0-.88-.31-1.59-.82-2.15.08-.2.36-1.02-.08-2.12 0 0-.67-.22-2.2.82-.64-.18-1.32-.27-2-.27-.68 0-1.36.09-2 .27-1.53-1.03-2.2-.82-2.2-.82-.44 1.1-.16 1.92-.08 2.12-.51.56-.82 1.28-.82 2.15 0 3.06 1.86 3.75 3.64 3.95-.23.2-.44.55-.51 1.07-.46.21-1.61.55-2.33-.66-.15-.24-.6-.83-1.23-.82-.67.01-.27.38.01.53.34.19.73.9.82 1.13.16.45.68 1.31 2.69.94 0 .67.01 1.3.01 1.49 0 .21-.15.45-.55.38A7.995 7.995 0 0 1 0 8c0-4.42 3.58-8 8-8Z" />
    </svg>
  );
}
