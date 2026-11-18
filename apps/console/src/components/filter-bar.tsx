import { cn } from "@veristar/brand/cn";
import { Search, SlidersHorizontal, X } from "lucide-react";
import { type ReactNode, useEffect, useId, useMemo, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

export type Filter = {
  key: string;
  label: string;
  // the control itself, with its own <label>: a native select, a checkbox,
  // a segmented control. it applies its value at once; there is no Apply.
  control: ReactNode;
  // set while the filter narrows the list: the chip text and how to undo it
  active?: { label: string; onClear: () => void } | null;
};

// the phone breakpoint of the console (tailwind sm). the filter controls
// render in one place at a time: in the row on a wide screen, in a sheet on
// a phone, so each control and its label exist once in the page.
const PHONE = "(max-width: 639.98px)";

function usePhone(): boolean {
  const query = useMemo(
    () => (typeof window !== "undefined" ? window.matchMedia?.(PHONE) : undefined),
    [],
  );
  const [phone, setPhone] = useState(() => query?.matches ?? false);
  useEffect(() => {
    if (!query) return;
    const on = () => setPhone(query.matches);
    query.addEventListener("change", on);
    return () => query.removeEventListener("change", on);
  }, [query]);
  return phone;
}

// search, filters and the active filter chips of a list, in one row. every
// change applies at once.
export function FilterBar({
  search,
  onSearch,
  searchLabel = "Search",
  placeholder = "Search",
  filters = [],
  onClearAll,
  className,
}: {
  search?: string;
  onSearch?: (value: string) => void;
  searchLabel?: string;
  placeholder?: string;
  filters?: Filter[];
  // clears the search and every filter; shown while anything is active
  onClearAll?: () => void;
  className?: string;
}) {
  const phone = usePhone();
  const [sheet, setSheet] = useState(false);
  const searchId = useId();
  const active = filters.filter((f) => f.active);
  const count = active.length;
  const anything = count > 0 || (search ?? "").trim() !== "";

  return (
    <div className={cn("flex flex-col gap-3", className)}>
      <div className="flex flex-wrap items-end gap-3">
        {onSearch ? (
          <div className="relative min-w-0 flex-1 basis-60">
            <label htmlFor={searchId} className="sr-only">
              {searchLabel}
            </label>
            <Search
              aria-hidden
              className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-fg-3"
            />
            <Input
              id={searchId}
              type="search"
              value={search ?? ""}
              placeholder={placeholder}
              onChange={(e) => onSearch(e.target.value)}
              className="pl-8 max-lg:h-(--vs-control-touch)"
            />
          </div>
        ) : null}
        {filters.length > 0 && !phone ? (
          <div className="flex flex-wrap items-end gap-3">
            {filters.map((f) => (
              <div key={f.key} className="min-w-0">
                {f.control}
              </div>
            ))}
          </div>
        ) : null}
        {filters.length > 0 && phone ? (
          <Button
            type="button"
            variant="outline"
            aria-expanded={sheet}
            onClick={() => setSheet(true)}
            className="h-(--vs-control-touch)"
          >
            <SlidersHorizontal aria-hidden />
            Filters
            {count > 0 ? (
              <span className="rounded-full bg-selected px-2 font-mono text-caption text-fg tabular">
                {count}
                <span className="sr-only"> active</span>
              </span>
            ) : null}
          </Button>
        ) : null}
      </div>

      {anything ? (
        <div className="flex flex-wrap items-center gap-2">
          {count > 0 ? (
            <ul aria-label="Active filters" className="flex flex-wrap items-center gap-2">
              {active.map((f) => (
                <li
                  key={f.key}
                  className="inline-flex h-7 animate-fade-in items-center gap-1 rounded-full border border-line-strong pr-1 pl-3 text-caption text-fg max-lg:h-(--vs-control-touch)"
                >
                  <span className="text-fg-2">{f.label}:</span>{" "}
                  <span className="font-medium">{f.active?.label}</span>
                  <button
                    type="button"
                    aria-label={`Remove filter ${f.label}`}
                    onClick={() => f.active?.onClear()}
                    className="ml-1 inline-flex size-5 items-center justify-center rounded-full text-fg-3 transition-colors duration-(--vs-duration-press) ease-standard hover:bg-hover hover:text-fg focus-ring active:bg-selected max-lg:size-8"
                  >
                    <X aria-hidden className="size-3" />
                  </button>
                </li>
              ))}
            </ul>
          ) : null}
          {onClearAll ? (
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={onClearAll}
              className="max-lg:h-(--vs-control-touch)"
            >
              Clear filters
            </Button>
          ) : null}
        </div>
      ) : null}

      {phone ? (
        <FilterSheet open={sheet} onClose={() => setSheet(false)}>
          {filters.map((f) => (
            <div key={f.key}>{f.control}</div>
          ))}
        </FilterSheet>
      ) : null}
    </div>
  );
}

// the phone's filter sheet: a native <dialog> (no injected style, which the
// console's csp refuses) that rises from the bottom edge
function FilterSheet({
  open,
  onClose,
  children,
}: {
  open: boolean;
  onClose: () => void;
  children: ReactNode;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const titleId = useId();
  useEffect(() => {
    const d = ref.current;
    if (!d) return;
    if (open && !d.open) d.showModal();
    if (!open && d.open) d.close();
  }, [open]);
  return (
    // biome-ignore lint/a11y/useKeyWithClickEvents: the click only closes on the scrim; Escape closes the native dialog
    <dialog
      ref={ref}
      aria-labelledby={titleId}
      onClose={onClose}
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
      className="dialog-motion mx-0 mt-auto mb-0 max-h-[85dvh] w-full max-w-none overflow-y-auto rounded-t-xl border border-line-strong bg-overlay p-0 text-fg shadow-dialog"
    >
      <div className="flex items-center justify-between gap-3 border-b border-line px-4 py-3">
        <h2 id={titleId} className="text-title">
          Filters
        </h2>
        <Button
          type="button"
          variant="ghost"
          size="icon-lg"
          aria-label="Close filters"
          onClick={onClose}
        >
          <X aria-hidden />
        </Button>
      </div>
      <div className="flex flex-col gap-4 p-4">{children}</div>
      <div className="border-t border-line p-4">
        <Button
          type="button"
          variant="outline"
          className="h-(--vs-control-touch) w-full"
          onClick={onClose}
        >
          Show results
        </Button>
      </div>
    </dialog>
  );
}
