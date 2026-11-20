import { useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router";
import { termsQuery } from "@/api/queries";
import { Disclosure } from "@/components/disclosure";
import { PublicFrame } from "@/components/public-frame";
import { ErrorState, LoadingState, Skeleton } from "@/components/states";
import { Pill } from "@/components/status";
import { Button } from "@/components/ui/button";
import { Markdown, outline, splitNumber } from "@/lib/markdown";
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
        <Button asChild variant="outline" size="sm">
          <Link to="/">Open the console</Link>
        </Button>
      }
    >
      <div className="grid w-full gap-10 lg:grid-cols-[15rem_minmax(0,700px)] lg:gap-16 lg:px-12">
        {sections.length > 0 ? (
          <nav
            aria-label="Sections"
            className="sticky top-6 hidden flex-col gap-0.5 self-start pt-2 text-small lg:flex"
          >
            <span className="mb-2 px-2.5 text-caption text-fg-2">On this page</span>
            {sections.map((s) => {
              const { n, title } = splitNumber(s.text);
              const on = current === s.id;
              return (
                <a
                  key={s.id}
                  href={`#${s.id}`}
                  aria-current={on ? "location" : undefined}
                  className={cn(
                    "flex gap-2.5 rounded-lg px-2.5 py-1.5 transition-colors duration-(--vs-duration-press) hover:bg-hover hover:text-fg focus-ring",
                    on ? "bg-selected text-fg" : "text-fg-2",
                  )}
                >
                  {n ? (
                    <span
                      className={cn(
                        "w-4 shrink-0 font-medium tabular",
                        on ? "text-fg-2" : "text-fg-3",
                      )}
                    >
                      {n}
                    </span>
                  ) : null}
                  <span>{title}</span>
                </a>
              );
            })}
          </nav>
        ) : (
          <span aria-hidden className="hidden lg:block" />
        )}
        <div className="flex min-w-0 flex-col gap-6">
          {sections.length > 0 ? (
            <Disclosure
              summary="On this page"
              className="lg:hidden"
              summaryClassName="font-normal"
              bodyClassName="border-t-0 px-2 pt-0 pb-2"
            >
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
            </Disclosure>
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
                  <p className="flex flex-wrap items-center gap-x-2.5 gap-y-1 text-small text-fg-2">
                    <span title={terms.data.version}>{termsVersionLabel(terms.data.version)}</span>
                    {isDraftTerms(terms.data.version, terms.data.status) ? (
                      <Pill tone="warning">Draft, not yet final</Pill>
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
