// "Find your SCF award" on the enrollment form (contract 4.2.7): a person
// types the project's name or website, sees up to 10 awards and picks one.
// the pick fills the form; the form sends its id (4.2.1).

import { useQuery } from "@tanstack/react-query";
import { Search } from "lucide-react";
import { useEffect, useId, useState } from "react";
import { ApiError } from "@/api/client";
import { api } from "@/api/endpoints";
import type { ScfAward } from "@/api/schemas";
import { Input } from "@/components/ui/input";
import { errorMessage } from "@/lib/errors";

// the query as the server reads it: white space collapsed, 2 to 100 characters
export function awardQuery(text: string): string | null {
  const q = text.trim().replace(/\s+/g, " ");
  return q.length >= 2 && q.length <= 100 ? q : null;
}

const DEBOUNCE_MS = 250;

export function AwardSearch({ onPick }: { onPick: (a: ScfAward) => void }) {
  const id = useId();
  const [text, setText] = useState("");
  const [q, setQ] = useState<string | null>(null);
  // a 429 holds the search until its Retry-After has passed
  const [waitUntil, setWaitUntil] = useState(0);
  const [, setTick] = useState(0);

  useEffect(() => {
    const t = setTimeout(() => setQ(awardQuery(text)), DEBOUNCE_MS);
    return () => clearTimeout(t);
  }, [text]);

  const waiting = waitUntil > Date.now();
  useEffect(() => {
    if (!waiting) return;
    const t = setTimeout(() => setTick((n) => n + 1), waitUntil - Date.now());
    return () => clearTimeout(t);
  }, [waiting, waitUntil]);

  const results = useQuery({
    queryKey: ["scf-awards", "search", q],
    queryFn: ({ signal }) => api.searchScfAwards(q ?? "", signal),
    enabled: q !== null && !waiting,
    retry: false,
    staleTime: 60_000,
  });
  const err = results.error;
  useEffect(() => {
    if (err instanceof ApiError && err.code === "rate_limited")
      setWaitUntil(Date.now() + (err.retryAfter ?? 60) * 1000);
  }, [err]);

  const open = q !== null && (results.data !== undefined || results.error !== null);
  const items = results.data?.items ?? [];
  return (
    <div className="flex min-w-0 flex-col gap-2" data-award-search="">
      <label htmlFor={id} className="text-small font-medium text-fg">
        Find your SCF award
      </label>
      <div className="relative">
        <Search
          aria-hidden
          className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-fg-3"
        />
        <Input
          id={id}
          type="search"
          role="combobox"
          aria-expanded={open}
          aria-controls={`${id}-list`}
          aria-autocomplete="list"
          autoComplete="off"
          spellCheck={false}
          maxLength={100}
          placeholder="Project name or website"
          className="pl-10 max-lg:h-(--vs-control-touch)"
          value={text}
          onChange={(e) => setText(e.target.value)}
        />
      </div>
      {open ? (
        <ul
          id={`${id}-list`}
          aria-label="SCF awards"
          className="flex flex-col divide-y divide-line-subtle rounded-lg border border-line bg-raised"
        >
          {results.error ? (
            <li className="px-3 py-2 text-small text-fg-2" data-award-state="error">
              {errorMessage(results.error)}
            </li>
          ) : items.length === 0 ? (
            <li className="px-3 py-2 text-small text-fg-3" data-award-state="empty">
              No match
            </li>
          ) : (
            items.map((a) => (
              <li key={a.id}>
                <button
                  type="button"
                  data-award={a.id}
                  onClick={() => {
                    onPick(a);
                    setText("");
                    setQ(null);
                  }}
                  className="flex w-full min-w-0 flex-wrap items-baseline justify-between gap-x-3 gap-y-1 px-3 py-2 text-left transition-colors duration-(--vs-duration-press) ease-standard hover:bg-hover focus-ring max-lg:min-h-(--vs-control-touch)"
                >
                  <span className="min-w-0 truncate text-body font-medium text-fg">
                    {a.project}
                  </span>
                  <span className="flex gap-3 text-small text-fg-2">
                    {a.round ? <span>{a.round}</span> : null}
                    {a.category ? <span>{a.category}</span> : null}
                  </span>
                </button>
              </li>
            ))
          )}
        </ul>
      ) : null}
    </div>
  );
}

// the cohort the award carries, in the form's cohort fields
export function awardCohort(a: ScfAward) {
  return {
    program: a.program ?? "",
    round: a.round,
    category: a.category ?? "",
    award_date: a.award_date ?? "",
  };
}
