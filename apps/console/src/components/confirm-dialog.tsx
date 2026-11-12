import { type ReactNode, useEffect, useId, useRef } from "react";
import { FormMessage } from "@/components/error-panel";
import { Button } from "@/components/ui/button";

// a confirmation built on the native <dialog> element: showModal() gives the
// top layer, an inert page behind it, focus inside and Escape to close. the
// radix dialog injects a <style> element for its scroll lock, which the
// console's csp (style-src 'self') refuses, so it is not used.
export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel,
  pendingLabel,
  cancelLabel = "Cancel",
  pending,
  error,
  onConfirm,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description: ReactNode;
  confirmLabel: string;
  pendingLabel: string;
  cancelLabel?: string;
  pending: boolean;
  error: unknown;
  onConfirm: () => void;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const titleId = useId();
  const descId = useId();

  useEffect(() => {
    const d = ref.current;
    if (!d) return;
    if (open && !d.open) d.showModal();
    if (!open && d.open) d.close();
  }, [open]);

  return (
    <dialog
      ref={ref}
      aria-labelledby={titleId}
      aria-describedby={descId}
      onClose={() => onOpenChange(false)}
      className="m-auto w-[min(28rem,calc(100vw-2rem))] rounded-xl border bg-popover p-5 text-popover-foreground shadow-xl backdrop:bg-foreground/40"
    >
      <h2 id={titleId} className="text-lg font-semibold tracking-tight">
        {title}
      </h2>
      <div id={descId} className="mt-2 text-sm text-muted-foreground">
        {description}
      </div>
      <div className="mt-3">
        <FormMessage error={error} />
      </div>
      <div className="mt-5 flex flex-wrap justify-end gap-2">
        <Button variant="outline" onClick={() => onOpenChange(false)} autoFocus>
          {cancelLabel}
        </Button>
        <Button
          className="bg-destructive text-white hover:bg-destructive/90"
          disabled={pending}
          onClick={onConfirm}
        >
          {pending ? pendingLabel : confirmLabel}
        </Button>
      </div>
    </dialog>
  );
}
