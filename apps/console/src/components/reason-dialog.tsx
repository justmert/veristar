import { type FormEvent, type ReactNode, useEffect, useState } from "react";
import { FormMessage } from "@/components/error-panel";
import { TextAreaField } from "@/components/form-field";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import * as v from "@/lib/validate";

// a confirmation that asks for the operator's reason (contract 5.2). the
// reason is shown to the project's members, which the hint says.
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
  hint = "The project's members see this text.",
  maxLength = 1000,
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
  // who reads the reason; the roster's reasons stay in the audit log
  hint?: string;
  maxLength?: number;
}) {
  const [reason, setReason] = useState("");
  const [fieldError, setFieldError] = useState<string | null>(null);

  useEffect(() => {
    if (open) {
      setReason("");
      setFieldError(null);
    }
  }, [open]);

  function submit(e: FormEvent) {
    e.preventDefault();
    const code = v.reason(reason, reasonRequired);
    setFieldError(code ? v.fieldMessage(code) : null);
    if (code) return;
    onConfirm(reason.trim());
  }

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={title}
      description={description}
      onSubmit={submit}
      footer={
        <>
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            type="submit"
            variant={tone === "destructive" ? "destructive" : "default"}
            pending={pending}
          >
            {pending ? pendingLabel : confirmLabel}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-3">
        <TextAreaField
          label="Reason"
          name="reason"
          optional={!reasonRequired}
          rows={3}
          maxLength={maxLength}
          hint={hint}
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          error={fieldError}
        />
        <FormMessage error={error} />
      </div>
    </Dialog>
  );
}
