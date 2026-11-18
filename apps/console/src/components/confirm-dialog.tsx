import { type FormEvent, type ReactNode, useEffect, useId, useState } from "react";
import { FormMessage } from "@/components/error-panel";
import { TextAreaField } from "@/components/form-field";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import * as v from "@/lib/validate";

export type ReasonOptions = {
  required: boolean;
  // who reads the reason, said under the field
  hint?: string;
  maxLength?: number;
  label?: string;
};

export type TypedConfirmation = {
  // the exact text the person types, such as a domain or a login
  value: string;
  label: string;
};

// the one confirmation for every consequential action. the title names the
// action and the object; the description says what happens. options: a
// reason field (operators: the members read it) and a typed confirmation
// for an action that cannot be undone. destructive actions use the solid
// critical button; Cancel has the focus, so Enter never confirms by accident.
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
  tone = "destructive",
  confirmDisabled = false,
  reason,
  typed,
  children,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  // the consequence, in one or two sentences
  description: ReactNode;
  confirmLabel: string;
  pendingLabel?: string;
  cancelLabel?: string;
  pending: boolean;
  error: unknown;
  // the trimmed reason when the dialog asks for one, otherwise ""
  onConfirm: (reason: string) => void;
  // a change that is easy to undo (a setting, an approval) is not drawn as
  // destructive
  tone?: "destructive" | "primary";
  // keeps the confirm button off until a check of the caller passes
  confirmDisabled?: boolean;
  reason?: ReasonOptions;
  typed?: TypedConfirmation;
  children?: ReactNode;
}) {
  const [text, setText] = useState("");
  const [typedText, setTypedText] = useState("");
  const [fieldError, setFieldError] = useState<string | null>(null);
  const typedId = useId();

  useEffect(() => {
    if (open) {
      setText("");
      setTypedText("");
      setFieldError(null);
    }
  }, [open]);

  const typedOk = !typed || typedText.trim() === typed.value;

  function submit(e: FormEvent) {
    e.preventDefault();
    if (pending || confirmDisabled || !typedOk) return;
    if (reason) {
      const code = v.reason(text, reason.required);
      setFieldError(code ? v.fieldMessage(code) : null);
      if (code) return;
    }
    onConfirm(reason ? text.trim() : "");
  }

  const hasBody = children || reason || typed || error;
  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      size="sm"
      title={title}
      description={description}
      onSubmit={submit}
      footer={
        <>
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)} autoFocus>
            {cancelLabel}
          </Button>
          <Button
            type="submit"
            variant={tone === "destructive" ? "destructive" : "default"}
            pending={pending}
            disabled={confirmDisabled || !typedOk}
          >
            {pending && pendingLabel ? pendingLabel : confirmLabel}
          </Button>
        </>
      }
    >
      {hasBody ? (
        <div className="flex flex-col gap-4">
          {children}
          {reason ? (
            <TextAreaField
              label={reason.label ?? "Reason"}
              name="reason"
              optional={!reason.required}
              rows={3}
              maxLength={reason.maxLength ?? 1000}
              hint={reason.hint ?? "The project's members see this text."}
              value={text}
              onChange={(e) => {
                setText(e.target.value);
                if (fieldError) setFieldError(null);
              }}
              error={fieldError}
            />
          ) : null}
          {typed ? (
            <div className="flex flex-col gap-1">
              <label htmlFor={typedId} className="text-small font-medium text-fg">
                {typed.label}
              </label>
              <Input
                id={typedId}
                autoComplete="off"
                spellCheck={false}
                value={typedText}
                onChange={(e) => setTypedText(e.target.value)}
                className="font-mono"
              />
            </div>
          ) : null}
          <FormMessage error={error} />
        </div>
      ) : null}
    </Dialog>
  );
}
