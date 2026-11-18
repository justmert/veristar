import { useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { ChevronRight } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router";
import { termsQuery } from "@/api/queries";
import { PublicFrame } from "@/components/public-frame";
import { ErrorState, LoadingState, Skeleton } from "@/components/states";
import { Markdown, outline } from "@/lib/markdown";
import { isDraftTerms, termsVersionLabel } from "@/lib/terms";

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
        <div className="flex min-w-0 max-w-prose flex-col gap-6">
          {sections.length > 0 ? (
            <details className="group rounded-lg border border-line lg:hidden">
              <summary className="flex min-h-(--vs-control-touch) cursor-pointer list-none items-center gap-2 rounded-lg px-4 text-body text-fg focus-ring [&::-webkit-details-marker]:hidden">
                <ChevronRight
                  aria-hidden
                  className="size-4 text-fg-2 transition-transform duration-(--vs-duration-press) ease-standard group-open:rotate-90"
                />
                On this page
              </summary>
              <nav aria-label="Sections on this page" className="flex flex-col gap-px px-2 pb-2">
                {sections.map((s) => (
                  <a
                    key={s.id}
                    href={`#${s.id}`}
                    onClick={(e) => e.currentTarget.closest("details")?.removeAttribute("open")}
                    className="flex min-h-(--vs-control-touch) items-center rounded-md px-2 text-body text-fg-2 transition-colors duration-(--vs-duration-press) hover:bg-hover hover:text-fg focus-ring"
                  >
                    {s.text}
                  </a>
                ))}
              </nav>
            </details>
          ) : null}
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
                  <p className="flex flex-wrap items-center gap-x-2 gap-y-1 text-small text-fg-2">
                    <span title={terms.data.version}>{termsVersionLabel(terms.data.version)}</span>
                    {isDraftTerms(terms.data.version, terms.data.status) ? (
                      <>
                        <span aria-hidden>·</span>
                        <span>Draft, not yet final</span>
                      </>
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
