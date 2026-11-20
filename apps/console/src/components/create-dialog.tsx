import { Plus } from "lucide-react";
import { type FormEvent, type ReactNode, useState } from "react";
import { FormMessage } from "@/components/error-panel";
import { SecretPanel } from "@/components/secret-panel";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";

// the secret a create returns once (a key, a token): it replaces the form
// inside the same dialog
export type CreatedSecret = {
  value: string;
  // "key", "token": the dialog's title reads "Copy your new key"
  noun: string;
  // the name the person gave it
  name: string;
  copyLabel?: string;
  // more for the person to copy with it (a server address, a client guide)
  children?: ReactNode;
};

// the one create pattern: the page header's primary button opens a dialog
// with the form. a create that returns a one-time secret shows it in the
// same dialog, which then closes only once the person ticks that they
// stored it. the caller keeps the form state, validates on submit and
// passes the mutation's pending and error.
export function CreateDialog({
  label,
  icon = <Plus aria-hidden />,
  title,
  description,
  submitLabel,
  pendingLabel,
  pending,
  error,
  onSubmit,
  secret,
  onClosed,
  open: openProp,
  onOpenChange,
  trigger = true,
  disabled,
  size = "md",
  children,
}: {
  // the header button's text ("Create key")
  label: string;
  icon?: ReactNode;
  // the dialog's title; the button's text when left out
  title?: string;
  description?: ReactNode;
  submitLabel?: string;
  pendingLabel?: string;
  pending: boolean;
  error: unknown;
  onSubmit: (e: FormEvent<HTMLFormElement>) => void;
  secret?: CreatedSecret | null;
  // the dialog closed: reset the form, the mutation and the secret here
  onClosed?: () => void;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  // false: no header button (the caller opens the dialog itself)
  trigger?: boolean;
  disabled?: boolean;
  size?: "sm" | "md";
  children: ReactNode;
}) {
  const [openOwn, setOpenOwn] = useState(false);
  const [stored, setStored] = useState(false);
  const open = openProp ?? openOwn;
  function setOpen(o: boolean) {
    // a secret shown once stays until the person says it is stored
    if (!o && secret && !stored) return;
    if (!o && pending) return;
    setOpenOwn(o);
    onOpenChange?.(o);
    if (!o) {
      setStored(false);
      onClosed?.();
    }
  }
  return (
    <>
      {trigger ? (
        <Button
          onClick={() => setOpen(true)}
          disabled={disabled}
          className="max-lg:h-(--vs-control-touch)"
        >
          {icon}
          {label}
        </Button>
      ) : null}
      <Dialog
        open={open}
        onOpenChange={setOpen}
        size={size}
        title={secret ? `Copy your new ${secret.noun}` : (title ?? label)}
        description={secret ? secret.name : description}
        onSubmit={
          secret
            ? undefined
            : (e) => {
                e.preventDefault();
                if (!pending) onSubmit(e);
              }
        }
        footer={
          secret ? (
            <Button type="button" disabled={!stored} onClick={() => setOpen(false)}>
              Done
            </Button>
          ) : (
            <>
              <Button type="button" variant="ghost" onClick={() => setOpen(false)}>
                Cancel
              </Button>
              <Button type="submit" pending={pending}>
                {pending && pendingLabel ? pendingLabel : (submitLabel ?? label)}
              </Button>
            </>
          )
        }
      >
        {secret ? (
          <SecretPanel
            plain
            value={secret.value}
            noun={secret.noun}
            name={secret.name}
            copyLabel={secret.copyLabel}
            stored={stored}
            onStoredChange={setStored}
          >
            {secret.children}
          </SecretPanel>
        ) : (
          <div className="flex flex-col gap-4">
            {children}
            <FormMessage error={error} />
          </div>
        )}
      </Dialog>
    </>
  );
}
