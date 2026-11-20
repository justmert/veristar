import { cn } from "@veristar/brand/cn";
import { X } from "lucide-react";
import type * as React from "react";
import { type ReactNode, useEffect, useId, useRef } from "react";
import { Button } from "./button";

// a modal on the native <dialog>: showModal() gives the top layer, an inert
// page, focus inside and Escape to close. radix dialog injects a <style>
// element for its scroll lock, which the console's csp (style-src 'self')
// refuses. it grows in over a fading scrim with css only (dialog-motion).
const PANEL = "flex flex-col rounded-xl border border-line-2 bg-overlay shadow-dialog";
const WIDTH = { sm: "max-w-dialog-sm", md: "max-w-dialog" } as const;

// the first focus inside an opened dialog: a field the caller marked, else
// the first field of the body, else the first button of the footer (Cancel
// in a confirmation), never the close button, so a ring does not land on
// the corner and Enter never confirms by accident
// on a touch screen a focused field opens the keyboard over the dialog
// before the person has read it, so focus starts on the footer there
function initialFocus(d: HTMLDialogElement) {
  const touch = window.matchMedia?.("(pointer: coarse)").matches ?? false;
  const field = touch
    ? null
    : (d.querySelector<HTMLElement>("[data-autofocus]") ??
      d.querySelector<HTMLElement>(
        "[data-dialog-body] :is(input:not([type=hidden]), textarea, select):not(:disabled)",
      ));
  const target =
    field ?? d.querySelector<HTMLElement>("[data-dialog-footer] button:not(:disabled)");
  target?.focus({ preventScroll: true });
}

export function Dialog({
  open,
  onOpenChange,
  title,
  description,
  children,
  footer,
  size = "md",
  className,
  closeLabel = "Close",
  onSubmit,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: ReactNode;
  description?: ReactNode;
  children?: ReactNode;
  footer?: ReactNode;
  size?: "sm" | "md";
  className?: string;
  closeLabel?: string;
  // the panel becomes a form, so the footer's submit button submits it
  onSubmit?: (e: React.FormEvent<HTMLFormElement>) => void;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const titleId = useId();
  const descId = useId();

  // the open prop decides; escape only asks. the latest value is read after
  // a native close, so a caller that refuses to close (a ceremony is
  // running) gets its dialog back instead of a closed one it believes open
  const openRef = useRef(open);
  openRef.current = open;

  useEffect(() => {
    const d = ref.current;
    if (!d) return;
    if (open && !d.open) {
      d.showModal();
      initialFocus(d);
    }
    if (!open && d.open) d.close();
  }, [open]);

  const body = (
    <>
      <div className="flex items-start gap-3 px-5 pt-5">
        <div className="flex min-w-0 flex-1 flex-col gap-1">
          <h2 id={titleId} className="text-title leading-6 font-semibold text-fg">
            {title}
          </h2>
          {description ? (
            <div id={descId} className="text-body leading-5.25 text-fg-2">
              {description}
            </div>
          ) : null}
        </div>
        {/* as on the canvas a dialog with a footer has no corner close:
            Cancel, Escape and the scrim close it. one without a footer keeps
            the close button, so it always has a visible way out */}
        {footer ? null : (
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            aria-label={closeLabel}
            className="-mr-2 -mt-1"
            onClick={() => onOpenChange(false)}
          >
            <X aria-hidden />
          </Button>
        )}
      </div>
      {children ? (
        <div data-dialog-body="" className="px-5 pt-4 pb-1">
          {children}
        </div>
      ) : null}
      {footer ? (
        <div
          data-dialog-footer=""
          className="mt-4 flex flex-wrap justify-end gap-2 border-t border-line px-5 py-3.5"
        >
          {footer}
        </div>
      ) : (
        <div className="h-5" />
      )}
    </>
  );

  return (
    // biome-ignore lint/a11y/useKeyWithClickEvents: the click only closes on the scrim; the keyboard closes with Escape, which the native dialog handles
    <dialog
      ref={ref}
      aria-labelledby={titleId}
      aria-describedby={description ? descId : undefined}
      onCancel={(e) => {
        // escape: keep the dialog open and let the caller decide
        e.preventDefault();
        onOpenChange(false);
      }}
      onClose={() => {
        onOpenChange(false);
        // a browser may close on a repeated escape despite the cancel above
        setTimeout(() => {
          const d = ref.current;
          if (d && openRef.current && !d.open) d.showModal();
        }, 0);
      }}
      onClick={(e) => {
        // a click on the scrim: the dialog box or the gutter around the panel
        const t = e.target as HTMLElement;
        if (t === e.currentTarget || t.dataset.dialogGutter !== undefined) onOpenChange(false);
      }}
      // the transparent dialog box covers the whole viewport and scrolls when
      // the panel is taller than it. the box is never smaller than the
      // viewport, so its own edge (which clips) never cuts the panel's
      // shadow into a visible rectangle. the panel sits centred in a frame
      // that keeps the 16 px gutter on a phone
      className="dialog-motion fixed inset-0 m-0 h-dvh max-h-none w-full max-w-none overflow-y-auto overscroll-contain bg-transparent p-0 text-fg open:flex"
    >
      <div data-dialog-gutter="" className={cn("m-auto w-full p-4", WIDTH[size], className)}>
        {onSubmit ? (
          <form noValidate onSubmit={onSubmit} className={PANEL}>
            {body}
          </form>
        ) : (
          <div className={PANEL}>{body}</div>
        )}
      </div>
    </dialog>
  );
}
