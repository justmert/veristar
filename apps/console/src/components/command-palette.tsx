import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { CornerDownLeft, FolderOpen, Search, X } from "lucide-react";
import { type KeyboardEvent, useEffect, useId, useMemo, useRef, useState } from "react";
import { useLocation, useNavigate } from "react-router";
import { stepUpQuery } from "@/api/operators";
import { operatorTenantsQuery } from "@/api/queries";
import type { Me } from "@/api/schemas";
import { areaOf, HOME_NAV, OPERATOR_NAV, projectNav } from "@/components/nav/nav-items";
import { useOperatorAdmin } from "@/components/nav/operator-role";
import { Kbd } from "@/components/panel";
import { Button } from "@/components/ui/button";

// the group header carries the context ("Lumen Pay", "Operator"), so a row
// says only the page or project name
type Entry = { key: string; group: string; label: string; detail?: string; to: string };

// ctrl k or cmd k opens it from any signed-in page; the shell owns the key
export function isPaletteKey(e: { key: string; ctrlKey: boolean; metaKey: boolean }) {
  return (e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "k";
}

// jump to any page and any project by typing part of its name. pages are
// those of the project in view, the console pages and, for operators, the
// operator pages; projects are the person's own and, for operators with a
// step-up, every project in the operator list. a native <dialog> (csp).
export function CommandPalette({
  me,
  open,
  onOpenChange,
}: {
  me: Me;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const input = useRef<HTMLInputElement>(null);
  const navigate = useNavigate();
  const { pathname } = useLocation();
  const [q, setQ] = useState("");
  const [active, setActive] = useState(0);
  const listId = useId();
  const operator = me.user.is_operator;
  const stepUp = useQuery({ ...stepUpQuery, enabled: open && operator });
  const live = open && operator && stepUp.data?.stepped_up === true;
  // alerts are for operator admins only
  const admin = useOperatorAdmin(open) === true;
  const term = q.trim();
  const opList = useInfiniteQuery({
    ...operatorTenantsQuery({
      status: [],
      review: false,
      q: term,
      sort: "name",
      order: "asc",
    }),
    enabled: live,
  });

  useEffect(() => {
    const d = ref.current;
    if (!d) return;
    if (open && !d.open) {
      setQ("");
      setActive(0);
      d.showModal();
      input.current?.focus();
    }
    if (!open && d.open) d.close();
  }, [open]);

  const entries = useMemo(() => {
    const out: Entry[] = [];
    const area = areaOf(pathname);
    const projectId = area.kind === "project" ? area.id : (me.tenants[0]?.id ?? null);
    const projectName = me.tenants.find((t) => t.id === projectId)?.name;
    if (projectId)
      for (const g of projectNav(projectId))
        for (const i of g.items)
          out.push({
            key: `p-${i.key}`,
            group: projectName ? `Pages of ${projectName}` : "Project pages",
            label: i.label,
            to: i.to,
          });
    for (const i of HOME_NAV)
      out.push({ key: `h-${i.key}`, group: "Console", label: i.label, to: i.to });
    out.push({ key: "h-new", group: "Console", label: "Enroll a project", to: "/projects/new" });
    if (operator)
      for (const g of OPERATOR_NAV)
        for (const i of g.items)
          if (i.key !== "alerts" || admin)
            out.push({ key: `o-${i.key}`, group: "Operator pages", label: i.label, to: i.to });
    for (const t of me.tenants)
      out.push({
        key: `t-${t.id}`,
        group: "My projects",
        label: t.name,
        to: `/projects/${t.id}`,
      });
    if (live)
      for (const t of opList.data?.pages.flatMap((p) => p.items) ?? [])
        out.push({
          key: `ot-${t.id}`,
          group: "All projects (operator)",
          label: t.name,
          detail: t.slug,
          to: `/operator/projects/${t.id}`,
        });
    const lower = term.toLowerCase();
    // the operator list is already searched by the server
    return out.filter(
      (e) =>
        e.group === "All projects (operator)" ||
        !lower ||
        e.label.toLowerCase().includes(lower) ||
        (e.detail ?? "").toLowerCase().includes(lower),
    );
  }, [pathname, me.tenants, operator, admin, live, opList.data, term]);

  const at = Math.min(active, Math.max(0, entries.length - 1));

  function go(e: Entry | undefined) {
    if (!e) return;
    onOpenChange(false);
    navigate(e.to);
  }

  function keys(e: KeyboardEvent) {
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      const step = e.key === "ArrowDown" ? 1 : -1;
      setActive((a) => (Math.min(a, entries.length - 1) + step + entries.length) % entries.length);
    } else if (e.key === "Enter") {
      e.preventDefault();
      go(entries[at]);
    }
  }

  let lastGroup = "";
  return (
    // biome-ignore lint/a11y/useKeyWithClickEvents: the click only closes on the scrim; Escape closes the native dialog
    <dialog
      ref={ref}
      aria-label="Go to a page or a project"
      onClose={() => onOpenChange(false)}
      onCancel={(e) => {
        e.preventDefault();
        onOpenChange(false);
      }}
      onClick={(e) => {
        if (e.target === e.currentTarget) onOpenChange(false);
      }}
      // overflow-visible: a modal dialog clips to its box by default, which
      // would cut the panel's shadow into a visible rectangle
      className="dialog-motion mx-auto mt-16 mb-auto max-h-[70dvh] w-full max-w-dialog overflow-visible bg-transparent p-4 text-fg max-sm:mt-4"
    >
      <div className="flex max-h-[calc(70dvh-2rem)] flex-col overflow-hidden rounded-[14px] border border-line-2 bg-overlay shadow-dialog">
        <div className="flex items-center gap-2 border-b border-line px-4">
          <Search aria-hidden className="size-4 shrink-0 text-fg-3" />
          <input
            ref={input}
            role="combobox"
            aria-expanded="true"
            aria-controls={listId}
            aria-activedescendant={entries[at] ? `${listId}-${entries[at].key}` : undefined}
            aria-label="Search pages and projects"
            placeholder="Search pages and projects"
            value={q}
            onChange={(e) => {
              setQ(e.target.value);
              setActive(0);
            }}
            onKeyDown={keys}
            className="h-12 min-w-0 flex-1 bg-transparent text-body text-fg outline-none placeholder:text-fg-3"
          />
          <span className="max-sm:hidden">
            <Kbd>Esc</Kbd>
          </span>
          <Button
            variant="ghost"
            size="icon-lg"
            aria-label="Close"
            className="-mr-2 sm:hidden"
            onClick={() => onOpenChange(false)}
          >
            <X aria-hidden />
          </Button>
        </div>
        <div
          id={listId}
          role="listbox"
          aria-label="Results"
          className="scroll-fade min-h-0 overflow-y-auto p-2"
        >
          {entries.length === 0 ? (
            <p role="status" className="px-2 py-6 text-center text-small text-fg-2">
              No page or project matches. Try another name.
            </p>
          ) : (
            entries.map((e, i) => {
              const head = e.group !== lastGroup ? e.group : null;
              lastGroup = e.group;
              return (
                <div key={e.key} role="presentation">
                  {head ? (
                    <div role="presentation" className="px-2 pt-2 pb-1 text-caption text-fg-3">
                      {head}
                    </div>
                  ) : null}
                  {/* biome-ignore lint/a11y/useFocusableInteractive: the options are reached with the arrow keys from the search field (aria-activedescendant) */}
                  {/* biome-ignore lint/a11y/useKeyWithClickEvents: Enter in the search field opens the active option */}
                  <div
                    id={`${listId}-${e.key}`}
                    role="option"
                    aria-selected={i === at}
                    onPointerMove={() => setActive(i)}
                    onClick={() => go(e)}
                    className={cn(
                      "flex h-9 cursor-pointer items-center gap-2.5 rounded-[8px] px-2 text-body max-lg:h-(--vs-control-touch)",
                      i === at ? "bg-hover text-fg" : "text-fg-2",
                    )}
                  >
                    {e.key.startsWith("t-") || e.key.startsWith("ot-") ? (
                      <FolderOpen aria-hidden className="size-4 shrink-0 text-fg-3" />
                    ) : null}
                    <span className="min-w-0 flex-1 truncate">
                      <Highlight text={e.label} term={term} />
                    </span>
                    {e.detail ? (
                      <span className="max-w-40 shrink-0 truncate text-caption text-fg-3">
                        {e.detail}
                      </span>
                    ) : null}
                    {i === at ? (
                      <CornerDownLeft aria-hidden className="size-4 shrink-0 text-fg-3" />
                    ) : null}
                  </div>
                </div>
              );
            })
          )}
        </div>
        <div
          aria-hidden
          className="flex items-center gap-4 border-t border-line px-4 py-2 text-caption text-fg-3 max-sm:hidden"
        >
          <span className="flex items-center gap-1">
            <Kbd>↑</Kbd>
            <Kbd>↓</Kbd>
            Move
          </span>
          <span className="flex items-center gap-1">
            <Kbd>Enter</Kbd>
            Open
          </span>
          <span className="flex items-center gap-1">
            <Kbd>Esc</Kbd>
            Close
          </span>
        </div>
      </div>
    </dialog>
  );
}

// the typed part of a name, marked
function Highlight({ text, term }: { text: string; term: string }) {
  const at = term ? text.toLowerCase().indexOf(term.toLowerCase()) : -1;
  if (at < 0) return <>{text}</>;
  return (
    <>
      {text.slice(0, at)}
      <mark className="rounded-sm bg-transparent font-semibold text-fg underline decoration-accent-text underline-offset-2">
        {text.slice(at, at + term.length)}
      </mark>
      {text.slice(at + term.length)}
    </>
  );
}
