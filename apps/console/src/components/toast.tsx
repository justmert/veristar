import { cn } from "@veristar/brand/cn";
import { CircleAlert, CircleCheck, X } from "lucide-react";
import { useEffect, useRef, useSyncExternalStore } from "react";
import { Link } from "react-router";
import { Button } from "@/components/ui/button";

// a short status message after an action that closes a dialog or changes
// something (save, create, revoke, invite, approve, reject, suspend,
// remove). one region for the whole console, announced politely. a toast
// leaves after a few seconds; pointer or focus on it keeps it.

export type ToastAction = { label: string } & ({ onClick: () => void } | { to: string });

type Kind = "success" | "error";
type Item = { id: number; kind: Kind; message: string; action?: ToastAction };

// how long a toast stays, in milliseconds; an error stays longer
const STAY: Record<Kind, number> = { success: 5000, error: 9000 };
// one toast at a time: a new one replaces the last, so two never stack or
// contradict each other
const MAX = 1;

let items: Item[] = [];
let next = 1;
const listeners = new Set<() => void>();

function emit() {
  for (const l of listeners) l();
}

function push(kind: Kind, message: string, opts?: { action?: ToastAction }): number {
  const id = next++;
  items = [...items, { id, kind, message, action: opts?.action }].slice(-MAX);
  emit();
  return id;
}

export function dismissToast(id: number) {
  items = items.filter((t) => t.id !== id);
  emit();
}

export const toast = {
  success: (message: string, opts?: { action?: ToastAction }) => push("success", message, opts),
  error: (message: string, opts?: { action?: ToastAction }) => push("error", message, opts),
  // tests and sign-out start from an empty region
  clear: () => {
    items = [];
    emit();
  },
};

function subscribe(l: () => void) {
  listeners.add(l);
  return () => listeners.delete(l);
}

// mounted once, in the shell. the region is a manual popover where the
// browser has them, so a toast shows above an open <dialog> (the top layer)
export function Toaster() {
  const list = useSyncExternalStore(
    subscribe,
    () => items,
    () => items,
  );
  const ref = useRef<HTMLElement>(null);
  const newest = list.at(-1)?.id ?? 0;
  // each new toast lifts the region to the top of the top layer again, so
  // it shows above a dialog that opened after the region did
  useEffect(() => {
    const el = ref.current;
    if (!el || newest === 0 || typeof el.showPopover !== "function") return;
    try {
      if (el.matches(":popover-open")) el.hidePopover();
      el.showPopover();
    } catch {
      // a browser without the popover api keeps the fixed region
    }
  }, [newest]);
  return (
    <section
      ref={ref}
      popover="manual"
      aria-label="Notifications"
      // bottom right on a wide screen; on a phone it sits under the top bar,
      // clear of the form buttons and the home indicator at the bottom
      className="pointer-events-none fixed inset-auto right-0 bottom-0 z-(--vs-layer-toast) m-0 flex w-full max-w-dialog-sm flex-col items-stretch gap-2 overflow-visible border-0 bg-transparent p-4 text-fg max-sm:top-(--vs-header) max-sm:bottom-auto max-sm:max-w-none"
    >
      <ol aria-live="polite" className="flex flex-col gap-2">
        {list.map((t) => (
          <ToastItem key={t.id} item={t} />
        ))}
      </ol>
    </section>
  );
}

function ToastItem({ item }: { item: Item }) {
  const held = useRef(false);
  useEffect(() => {
    const timer = window.setInterval(() => {
      if (!held.current) dismissToast(item.id);
    }, STAY[item.kind]);
    return () => window.clearInterval(timer);
  }, [item.id, item.kind]);
  const hold = () => {
    held.current = true;
  };
  const release = () => {
    held.current = false;
  };
  const a = item.action;
  return (
    <li
      onPointerEnter={hold}
      onPointerLeave={release}
      onFocus={hold}
      onBlur={release}
      data-toast={item.kind}
      className={cn(
        "pointer-events-auto flex animate-rise-in items-start gap-2.5 rounded-lg border bg-overlay px-3.5 py-2.5 text-small shadow-overlay",
        item.kind === "error" ? "border-critical-line" : "border-line-2",
      )}
    >
      {item.kind === "error" ? (
        <CircleAlert aria-hidden className="mt-0.5 size-4 shrink-0 text-critical" />
      ) : (
        <CircleCheck aria-hidden className="mt-0.5 size-4 shrink-0 text-success" />
      )}
      <span className="min-w-0 flex-1 text-fg">{item.message}</span>
      {a ? (
        "to" in a ? (
          <Button asChild variant="link" size="sm" className="shrink-0">
            <Link to={a.to} onClick={() => dismissToast(item.id)}>
              {a.label}
            </Link>
          </Button>
        ) : (
          <Button
            variant="outline"
            size="xs"
            className="shrink-0"
            onClick={() => {
              a.onClick();
              dismissToast(item.id);
            }}
          >
            {a.label}
          </Button>
        )
      ) : null}
      <Button
        variant="ghost"
        size="icon-xs"
        aria-label="Dismiss"
        className="-mt-1 -mr-2 shrink-0 max-lg:size-(--vs-control-touch)"
        onClick={() => dismissToast(item.id)}
      >
        <X aria-hidden />
      </Button>
    </li>
  );
}
