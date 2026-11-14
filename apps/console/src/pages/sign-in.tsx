import { Link, useSearchParams } from "react-router";
import { signInUrl } from "@/api/endpoints";
import { Mark } from "@veristar/brand/mark";
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

  return (
    <main className="relative grid min-h-dvh place-items-center overflow-hidden bg-canvas px-4 py-12">
      <Orbits />
      <div className="relative w-full max-w-md">
        <div className="flex items-center gap-2 text-primary">
          <Mark className="size-9" />
          <span className="text-lg font-semibold tracking-tight text-foreground">Veristar</span>
        </div>
        <h1 className="mt-8 text-4xl font-semibold tracking-tight text-balance sm:text-5xl">
          Know who uses your Stellar project.
        </h1>
        <p className="mt-4 text-pretty text-muted-foreground">
          Page views and API traffic for teams in the Stellar ecosystem. No cookies on your
          visitors, no fees for anyone.
        </p>

        {message ? (
          <p
            role="alert"
            className="mt-6 rounded-lg border border-destructive/40 bg-destructive/5 p-3 text-sm text-destructive"
          >
            {message}
          </p>
        ) : null}

        <Button asChild size="lg" className="mt-8 h-11 w-full px-4 text-base sm:w-auto">
          <a href={signInUrl(returnTo)}>
            <GitHubMark />
            Sign in with GitHub
          </a>
        </Button>
        <p className="mt-4 text-sm text-muted-foreground">
          GitHub shares your public username and id. We ask for no email and no repository access.
          By enrolling a project you accept the{" "}
          <Link to="/terms" className="underline underline-offset-2 hover:text-foreground">
            terms
          </Link>
          .
        </p>
      </div>
    </main>
  );
}

// three tilted orbits behind the sign-in copy; decoration only
function Orbits() {
  return (
    <svg
      aria-hidden="true"
      className="pointer-events-none absolute -right-48 -top-40 size-[44rem] text-orbit sm:-right-24"
      viewBox="0 0 400 400"
      fill="none"
    >
      {[150, 112, 74].map((rx, i) => (
        <ellipse
          key={rx}
          cx="200"
          cy="200"
          rx={rx}
          ry={rx * 0.42}
          transform={`rotate(${-24 + i * 9} 200 200)`}
          stroke="currentColor"
          strokeWidth="1"
        />
      ))}
      <circle cx="200" cy="200" r="5" className="fill-primary" />
      <circle cx="318" cy="150" r="2.5" fill="currentColor" />
      <circle cx="120" cy="214" r="2" fill="currentColor" />
    </svg>
  );
}

// github's mark (octicons mark-github, mit licence); lucide no longer ships
// brand icons
function GitHubMark() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true" className="size-4" fill="currentColor">
      <path d="M8 0c4.42 0 8 3.58 8 8a8.013 8.013 0 0 1-5.45 7.59c-.4.08-.55-.17-.55-.38 0-.27.01-1.13.01-2.2 0-.75-.25-1.23-.54-1.48 1.78-.2 3.65-.88 3.65-3.95 0-.88-.31-1.59-.82-2.15.08-.2.36-1.02-.08-2.12 0 0-.67-.22-2.2.82-.64-.18-1.32-.27-2-.27-.68 0-1.36.09-2 .27-1.53-1.03-2.2-.82-2.2-.82-.44 1.1-.16 1.92-.08 2.12-.51.56-.82 1.28-.82 2.15 0 3.06 1.86 3.75 3.64 3.95-.23.2-.44.55-.51 1.07-.46.21-1.61.55-2.33-.66-.15-.24-.6-.83-1.23-.82-.67.01-.27.38.01.53.34.19.73.9.82 1.13.16.45.68 1.31 2.69.94 0 .67.01 1.3.01 1.49 0 .21-.15.45-.55.38A7.995 7.995 0 0 1 0 8c0-4.42 3.58-8 8-8Z" />
    </svg>
  );
}
