import type { ReactNode } from "react";
import { FormMessage } from "@/components/error-panel";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";

// one dialog shape for every consequential action: the title names the action
// and the object, the body says what happens, the confirm button repeats the
// verb. destructive actions use the solid red button.
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
  children,
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
  // a change that is easy to undo (a setting) is not drawn as destructive
  tone?: "destructive" | "primary";
  // keeps the confirm button off until the body's own check passes, such as
  // a typed confirmation
  confirmDisabled?: boolean;
  children?: ReactNode;
}) {
  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      size="sm"
      title={title}
      description={description}
      footer={
        <>
          <Button variant="outline" onClick={() => onOpenChange(false)} autoFocus>
            {cancelLabel}
          </Button>
          <Button
            variant={tone === "destructive" ? "destructive" : "default"}
            pending={pending}
            disabled={confirmDisabled}
            onClick={onConfirm}
          >
            {pending ? pendingLabel : confirmLabel}
          </Button>
        </>
      }
    >
      {children || error ? (
        <div className="flex flex-col gap-3">
          {children}
          <FormMessage error={error} />
        </div>
      ) : null}
    </Dialog>
  );
}
