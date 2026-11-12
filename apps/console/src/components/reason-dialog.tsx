import { type FormEvent, type ReactNode, useEffect, useId, useRef, useState } from "react";
import { FormMessage } from "@/components/error-panel";
import { TextAreaField } from "@/components/form-field";
import { Button } from "@/components/ui/button";
import * as v from "@/lib/validate";

// a confirmation that asks for the operator's reason (contract 5.2). it is a
// native <dialog> for the same csp reason as ConfirmDialog. the reason is
// shown to the project's members, which the hint says.
export function ReasonDialog({
  open,
  onOpenChange,
  title,
  description,
  reasonRequired,
  confirmLabel,
  pendingLabel,
  tone,
  pending,
  error,
  onConfirm,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description: ReactNode;
  reasonRequired: boolean;
  confirmLabel: string;
  pendingLabel: string;
  tone: "destructive" | "primary";
  pending: boolean;
  error: unknown;
  onConfirm: (reason: string) => void;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const titleId = useId();
  const descId = useId();
  const [reason, setReason] = useState("");
  const [fieldError, setFieldError] = useState<string | null>(null);

  useEffect(() => {
    const d = ref.current;
    if (!d) return;
    if (open && !d.open) {
      setReason("");
      setFieldError(null);
      d.showModal();
    }
    if (!open && d.open) d.close();
  }, [open]);

  function submit(e: FormEvent) {
    e.preventDefault();
    const code = v.reason(reason, reasonRequired);
    setFieldError(code ? v.fieldMessage(code) : null);
    if (code) return;
    onConfirm(reason.trim());
  }

  return (
    <dialog
      ref={ref}
      aria-labelledby={titleId}
      aria-describedby={descId}
      onClose={() => onOpenChange(false)}
      className="m-auto w-[min(32rem,calc(100vw-2rem))] rounded-xl border bg-popover p-5 text-popover-foreground shadow-xl backdrop:bg-foreground/40"
    >
      <form noValidate onSubmit={submit}>
        <h2 id={titleId} className="text-lg font-semibold tracking-tight">
          {title}
        </h2>
        <div id={descId} className="mt-2 text-sm text-muted-foreground">
          {description}
        </div>
        <div className="mt-4">
          <TextAreaField
            label="Reason"
            name="reason"
            optional={!reasonRequired}
            rows={3}
            maxLength={1000}
            hint="The project's members see this text."
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            error={fieldError}
          />
        </div>
        <div className="mt-3">
          <FormMessage error={error} />
        </div>
        <div className="mt-5 flex flex-wrap justify-end gap-2">
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            type="submit"
            className={
              tone === "destructive" ? "bg-destructive text-white hover:bg-destructive/90" : ""
            }
            disabled={pending}
          >
            {pending ? pendingLabel : confirmLabel}
          </Button>
        </div>
      </form>
    </dialog>
  );
}
