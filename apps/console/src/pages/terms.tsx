import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router";
import { termsQuery } from "@/api/queries";
import { ErrorPanel, Loading } from "@/components/error-panel";
import { Mark } from "@veristar/brand/mark";
import { Markdown } from "@/lib/markdown";

// public: people read the terms before they sign in and enroll
export function TermsPage() {
  const terms = useQuery(termsQuery);
  return (
    <div className="min-h-dvh bg-canvas">
      <header className="mx-auto flex max-w-3xl items-center gap-2 px-4 pt-8 sm:px-6">
        <Link to="/" className="flex items-center gap-2 rounded-md font-semibold tracking-tight">
          <Mark className="size-6 text-primary" />
          Veristar
        </Link>
      </header>
      <main id="main" className="mx-auto max-w-3xl px-4 py-8 sm:px-6">
        {terms.isPending ? <Loading label="Loading the terms" /> : null}
        {terms.error ? <ErrorPanel error={terms.error} onRetry={() => terms.refetch()} /> : null}
        {terms.data ? (
          <article className="max-w-prose rounded-xl border bg-card p-5 sm:p-8">
            <p className="mb-6 text-sm text-muted-foreground">
              Version <span className="font-medium text-foreground">{terms.data.version}</span>
              {terms.data.status === "draft" ? ", draft" : null}
            </p>
            <Markdown source={terms.data.markdown} />
          </article>
        ) : null}
      </main>
    </div>
  );
}
