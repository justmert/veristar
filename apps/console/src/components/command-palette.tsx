import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { CornerDownLeft, FolderOpen, Search } from "lucide-react";
import { type KeyboardEvent, useEffect, useId, useMemo, useRef, useState } from "react";
import { useLocation, useNavigate } from "react-router";
import { stepUpQuery } from "@/api/operators";
import { operatorTenantsQuery } from "@/api/queries";
import type { Me } from "@/api/schemas";
import { areaOf, HOME_NAV, OPERATOR_NAV, projectNav } from "@/components/nav/nav-items";
import { Kbd } from "@/components/panel";

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
            group: "Pages",
            label: i.label,
            detail: projectName,
            to: i.to,
          });
    for (const i of HOME_NAV)
      out.push({ key: `h-${i.key}`, group: "Pages", label: i.label, to: i.to });
    out.push({ key: "h-new", group: "Pages", label: "Enroll a project", to: "/projects/new" });
    if (operator)
      for (const g of OPERATOR_NAV)
        for (const i of g.items)
          out.push({
            key: `o-${i.key}`,
            group: "Pages",
            label: i.label,
            detail: "Operator",
            to: i.to,
          });
    for (const t of me.tenants)
      out.push({
        key: `t-${t.id}`,
        group: "Your projects",
        label: t.name,
        to: `/projects/${t.id}`,
      });
    if (live)
      for (const t of opList.data?.pages.flatMap((p) => p.items) ?? [])
        out.push({
          key: `ot-${t.id}`,
          group: "All projects",
          label: t.name,
          detail: t.slug,
          to: `/operator/projects/${t.id}`,
        });
    const lower = term.toLowerCase();
    // the operator list is already searched by the server
    return out.filter(
      (e) =>
        e.group === "All projects" ||
        !lower ||
        e.label.toLowerCase().includes(lower) ||
        (e.detail ?? "").toLowerCase().includes(lower),
    );
  }, [pathname, me.tenants, operator, live, opList.data, term]);

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
      className="dialog-motion mx-auto mt-16 mb-auto max-h-[70dvh] w-full max-w-dialog bg-transparent p-4 text-fg max-sm:mt-4"
    >
      <div className="flex max-h-[calc(70dvh-2rem)] flex-col overflow-hidden rounded-xl border border-line-strong bg-overlay shadow-dialog">
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
        </div>
        <div id={listId} role="listbox" aria-label="Results" className="overflow-y-auto p-2">
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
                    <div role="presentation" className="px-2 pt-2 pb-1 text-caption text-fg-2">
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
                      "flex h-9 cursor-pointer items-center gap-2 rounded-md px-2 text-body max-lg:h-(--vs-control-touch)",
                      i === at ? "bg-hover text-fg" : "text-fg-2",
                    )}
                  >
                    {e.group === "Pages" ? null : (
                      <FolderOpen aria-hidden className="size-4 shrink-0 text-fg-3" />
                    )}
                    <span className="min-w-0 flex-1 truncate">{e.label}</span>
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
      </div>
    </dialog>
  );
}
