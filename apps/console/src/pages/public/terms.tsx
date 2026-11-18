import { useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router";
import { termsQuery } from "@/api/queries";
import { PublicFrame } from "@/components/public-frame";
import { ErrorState, LoadingState, Skeleton } from "@/components/states";
import { StatusDot } from "@/components/status";
import { Markdown, outline } from "@/lib/markdown";

// public: people read the terms before they sign in and enroll
export function TermsPage() {
  const terms = useQuery(termsQuery);
  const sections = useMemo(() => (terms.data ? outline(terms.data.markdown) : []), [terms.data]);
  const current = useCurrentSection(sections.map((s) => s.id));

  return (
    <PublicFrame
      center={false}
      header={
        <Link
          to="/"
          className="rounded-md px-2 py-1 text-body text-fg-2 transition-colors duration-(--vs-duration-press) hover:text-fg focus-ring"
        >
          Open the console
        </Link>
      }
    >
      <div className="mx-auto grid w-full max-w-content gap-10 lg:grid-cols-[minmax(0,14rem)_minmax(0,1fr)] lg:gap-12">
        {sections.length > 0 ? (
          <nav
            aria-label="Sections"
            className="sticky top-6 hidden flex-col gap-px self-start lg:flex"
          >
            <span className="px-2 pb-2 text-caption text-fg-3">On this page</span>
            {sections.map((s) => (
              <a
                key={s.id}
                href={`#${s.id}`}
                aria-current={current === s.id ? "location" : undefined}
                className={cn(
                  "rounded-md px-2 py-1 text-body transition-colors duration-(--vs-duration-press) hover:bg-hover hover:text-fg focus-ring",
                  current === s.id ? "bg-selected text-fg" : "text-fg-2",
                )}
              >
                {s.text}
              </a>
            ))}
          </nav>
        ) : (
          <span aria-hidden className="hidden lg:block" />
        )}
        <div className="min-w-0 max-w-prose">
          {terms.isPending ? (
            <LoadingState label="Loading the terms">
              <Skeleton.Table rows={6} columns={1} />
            </LoadingState>
          ) : null}
          {terms.error ? (
            <ErrorState
              title="The terms did not load"
              error={terms.error}
              onRetry={() => terms.refetch()}
            />
          ) : null}
          {terms.data ? (
            <article className="animate-fade-in">
              <Markdown
                source={terms.data.markdown}
                afterTitle={
                  <p className="flex flex-wrap items-center gap-2 text-small text-fg-2">
                    <span>Version</span>
                    <span className="rounded-sm border border-line bg-sunken px-2 py-1 font-mono text-fg">
                      {terms.data.version}
                    </span>
                    {terms.data.status === "draft" ? (
                      <span className="flex items-center gap-1 text-fg-2">
                        <StatusDot tone="info" />
                        Draft
                      </span>
                    ) : null}
                  </p>
                }
              />
            </article>
          ) : null}
        </div>
      </div>
    </PublicFrame>
  );
}

// the section whose heading was last scrolled past the top third of the
// window; the first one until then
function useCurrentSection(ids: string[]): string | null {
  const [current, setCurrent] = useState<string | null>(null);
  const key = ids.join(" ");
  useEffect(() => {
    if (ids.length === 0 || typeof IntersectionObserver === "undefined") return;
    setCurrent(ids[0] ?? null);
    // the observer only says that a heading crossed the line; which one is
    // current is read from the positions of all of them
    const io = new IntersectionObserver(
      () => {
        const passed = ids.filter((id) => {
          const el = document.getElementById(id);
          return el ? el.getBoundingClientRect().top < window.innerHeight / 3 : false;
        });
        setCurrent(passed.at(-1) ?? ids[0] ?? null);
      },
      { rootMargin: "0px 0px -66% 0px", threshold: [0, 1] },
    );
    for (const id of ids) {
      const el = document.getElementById(id);
      if (el) io.observe(el);
    }
    return () => io.disconnect();
  }, [key]);
  return current;
}
