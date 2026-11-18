import { useMutation, useQuery } from "@tanstack/react-query";
import { KeyRound, Trash2 } from "lucide-react";
import { type FormEvent, useState } from "react";
import { ApiError } from "@/api/client";
import { operatorKeys, operatorsApi, type Passkey, passkeysQuery } from "@/api/operators";
import { keys } from "@/api/queries";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { TextField } from "@/components/form-field";
import { PasskeyDialog } from "@/components/passkey-dialog";
import { ErrorState } from "@/components/states";
import { AddPasskeyDialog, steppedUp } from "@/components/step-up";
import { toast } from "@/components/toast";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { formatDateTimeUtc } from "@/lib/format";
import { queryClient } from "@/query-client";

// the passkeys that confirm the operator area for the signed-in operator
// (contract 5.15). the list reads without a step-up; adding and deleting
// ask for one, which the dialogs below give. it lives on the account page,
// with the other ways the person proves who they are.

const NAME_MAX = 64;

function refresh() {
  void queryClient.invalidateQueries({ queryKey: operatorKeys.passkeys });
  void queryClient.invalidateQueries({ queryKey: operatorKeys.stepUp });
  void queryClient.invalidateQueries({ queryKey: keys.me });
}

// a passkey name the control plane takes: not empty, at most 64 characters
export function passkeyNameError(name: string): string | null {
  const n = name.trim();
  if (n === "") return "Enter a name.";
  if ([...n].length > NAME_MAX) return `Use at most ${NAME_MAX} characters.`;
  return null;
}

export function OperatorPasskeys({ userName }: { userName: string }) {
  const list = useQuery(passkeysQuery);
  const [name, setName] = useState("");
  const [nameError, setNameError] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);
  const [deleting, setDeleting] = useState<Passkey | null>(null);
  // a delete that waits for a passkey confirmation
  const [stepUp, setStepUp] = useState<Passkey | null>(null);

  const del = useMutation({
    mutationFn: (p: Passkey) => operatorsApi.deletePasskey(p.id),
    onSuccess: (_, p) => {
      setDeleting(null);
      toast.success(`${p.name} was deleted.`);
      refresh();
    },
    onError: (err, p) => {
      if (err instanceof ApiError && err.code === "step_up_required") {
        setDeleting(null);
        setStepUp(p);
      }
    },
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    const err = passkeyNameError(name);
    setNameError(err);
    if (!err) setAdding(true);
  }

  return (
    <section aria-labelledby="passkeys-h" className="flex flex-col gap-4">
      <div className="flex flex-col gap-1">
        <h2 id="passkeys-h" className="text-title text-fg">
          Your passkeys
        </h2>
        <p className="max-w-prose text-small text-fg-2">
          These passkeys open the operator area. Keep at least two, so a lost one does not lock you
          out.
        </p>
      </div>
      <div className="flex flex-col rounded-lg border border-line bg-raised">
        {list.isPending ? (
          <div role="status" aria-busy="true" className="flex flex-col">
            <span className="sr-only">Loading your passkeys</span>
            {[0, 1].map((i) => (
              <div
                key={i}
                aria-hidden
                className="flex h-(--vs-row) items-center gap-3 border-b border-line-subtle px-4 last:border-b-0"
              >
                <Skeleton className="h-3 w-1/3" />
              </div>
            ))}
          </div>
        ) : list.error ? (
          <div className="p-4">
            <ErrorState
              error={list.error}
              onRetry={() => list.refetch()}
              compact
              className="border-0"
            />
          </div>
        ) : (
          <ul aria-label="Your passkeys" className="flex flex-col">
            {list.data.sign_in_passkey ? (
              <li
                data-passkey="sign-in"
                className="flex flex-col gap-1 border-b border-line-subtle px-4 py-3 last:border-b-0"
              >
                <span className="text-body font-medium text-fg">Your sign-in passkey</span>
                <span className="text-small text-fg-2">
                  It also opens the operator area. Manage it under sign-in methods.
                </span>
              </li>
            ) : null}
            {list.data.items.map((p) => (
              <li
                key={p.id}
                data-passkey={p.id}
                className="flex flex-wrap items-center justify-between gap-3 border-b border-line-subtle px-4 py-3 last:border-b-0"
              >
                <span className="flex min-w-0 items-center gap-3">
                  <KeyRound aria-hidden className="size-4 shrink-0 text-fg-2" />
                  <span className="flex min-w-0 flex-col">
                    <span className="truncate text-body font-medium text-fg">{p.name}</span>
                    <span className="text-small text-fg-2">
                      Added {formatDateTimeUtc(p.created_at)}
                      {p.last_used_at ? `. Last used ${formatDateTimeUtc(p.last_used_at)}.` : "."}
                    </span>
                  </span>
                </span>
                <Button
                  size="sm"
                  variant="destructive-outline"
                  className="max-lg:h-(--vs-control-touch)"
                  onClick={() => {
                    del.reset();
                    setDeleting(p);
                  }}
                >
                  <Trash2 aria-hidden />
                  Delete
                </Button>
              </li>
            ))}
            {list.data.items.length === 0 && !list.data.sign_in_passkey ? (
              <li className="px-4 py-3 text-small text-fg-2">
                No passkey yet. Add one to open the operator area.
              </li>
            ) : null}
          </ul>
        )}
        <form
          noValidate
          onSubmit={submit}
          aria-label="Add a passkey"
          className="flex flex-wrap items-end gap-3 border-t border-line p-4"
        >
          <div className="min-w-0 flex-1 basis-60">
            <TextField
              label="Name of the new passkey"
              name="new-passkey"
              placeholder="Laptop"
              maxLength={NAME_MAX}
              autoComplete="off"
              value={name}
              error={nameError}
              onChange={(e) => {
                setName(e.target.value);
                if (nameError) setNameError(null);
              }}
              className="max-lg:h-(--vs-control-touch)"
            />
          </div>
          <Button type="submit" variant="outline" className="max-lg:h-(--vs-control-touch)">
            Add a passkey
          </Button>
        </form>
      </div>

      <AddPasskeyDialog
        open={adding}
        onOpenChange={setAdding}
        name={name.trim()}
        userName={userName}
        onAdded={() => {
          toast.success(`${name.trim()} was added.`);
          setName("");
          refresh();
        }}
      />
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(o) => {
          if (!o) {
            setDeleting(null);
            del.reset();
          }
        }}
        title={`Delete ${deleting?.name ?? "the passkey"}?`}
        description="This passkey no longer opens the operator area. Delete it from your device too."
        confirmLabel="Delete passkey"
        pendingLabel="Deleting"
        pending={del.isPending}
        error={del.error}
        onConfirm={() => deleting && del.mutate(deleting)}
      />
      <PasskeyDialog
        open={stepUp !== null}
        onOpenChange={(o) => {
          if (!o) setStepUp(null);
        }}
        mode="confirm"
        title="Confirm it is you"
        description="Deleting a passkey of the operator area needs a confirmation with another passkey."
        userName=""
        challenge={() => operatorsApi.stepUpChallenge()}
        verify={async (answer) => {
          const s = await operatorsApi.stepUp(answer);
          await steppedUp(s);
          const waiting = stepUp;
          setStepUp(null);
          if (waiting) del.mutate(waiting);
        }}
      />
    </section>
  );
}
