import { cn } from "@veristar/brand/cn";
import { X } from "lucide-react";
import type * as React from "react";
import { type ReactNode, useEffect, useId, useRef } from "react";
import { Button } from "./button";

// a modal on the native <dialog>: showModal() gives the top layer, an inert
// page, focus inside and Escape to close. radix dialog injects a <style>
// element for its scroll lock, which the console's csp (style-src 'self')
// refuses. it grows in over a fading scrim with css only (dialog-motion).
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
    if (open && !d.open) d.showModal();
    if (!open && d.open) d.close();
  }, [open]);

  const body = (
    <>
      <div className="flex items-start gap-3 px-5 pt-5">
        <div className="flex min-w-0 flex-1 flex-col gap-1">
          <h2 id={titleId} className="text-title text-fg">
            {title}
          </h2>
          {description ? (
            <div id={descId} className="text-body text-fg-2">
              {description}
            </div>
          ) : null}
        </div>
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
      </div>
      {children ? <div className="px-5 pt-4 pb-1">{children}</div> : null}
      {footer ? (
        <div className="mt-4 flex flex-wrap justify-end gap-2 border-t border-line px-5 py-3">
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
        // a click on the scrim (the dialog box itself, outside its panel)
        if (e.target === e.currentTarget) onOpenChange(false);
      }}
      className={cn(
        // the transparent dialog box pads the panel away from the viewport
        // edge on a phone and scrolls when the panel is taller than it
        "dialog-motion m-auto max-h-dvh w-full overflow-y-auto bg-transparent p-4 text-fg",
        size === "sm" ? "max-w-dialog-sm" : "max-w-dialog",
        className,
      )}
    >
      {onSubmit ? (
        <form
          noValidate
          onSubmit={onSubmit}
          className="flex flex-col rounded-xl border border-line-strong bg-overlay shadow-dialog"
        >
          {body}
        </form>
      ) : (
        <div className="flex flex-col rounded-xl border border-line-strong bg-overlay shadow-dialog">
          {body}
        </div>
      )}
    </dialog>
  );
}
