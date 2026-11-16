import { CircleCheck, KeyRound } from "lucide-react";
import { type ReactNode, useCallback, useEffect, useRef, useState } from "react";
import type { PasskeyAnswer } from "@/api/endpoints";
import type { PasskeyChallenge } from "@/api/schemas";
import { FormMessage } from "@/components/error-panel";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { answerChallenge, createPasskey, type NewPasskey, passkeysAvailable } from "@/lib/passkey";

// the console's passkey dialog (contract 4.1.9), for signing in and for
// linking a passkey to the signed-in account. the caller hands it the two
// control plane calls; the dialog runs the ceremonies of lib/passkey in
// between, each behind a click of its own:
//
//   <PasskeyDialog
//     open={open}
//     onOpenChange={setOpen}
//     mode="sign_in"
//     title="Continue with a passkey"
//     userName="Veristar account"
//     challenge={() => api.passkeyChallenge(returnTo)}
//     verify={async (answer) => { ... }}
//   />
//
// sign_in offers the passkey the browser holds for this console and the
// creation of a new one; link offers creation only, because a passkey the
// console already knows belongs to another account; confirm offers the
// passkeys the browser holds only, for a step-up of the signed-in account
// (contract 5.15).
//
// a challenge is fetched when the dialog opens, and again after every
// verification, which spends it.
export function PasskeyDialog({
  open,
  onOpenChange,
  mode,
  title,
  description,
  userName,
  challenge,
  verify,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  mode: "sign_in" | "link" | "confirm";
  title: ReactNode;
  description?: ReactNode;
  // what the browser's passkey list shows for a passkey created here
  userName: string;
  challenge: () => Promise<PasskeyChallenge>;
  verify: (answer: PasskeyAnswer) => Promise<void>;
}) {
  const [current, setCurrent] = useState<PasskeyChallenge | null>(null);
  const [created, setCreated] = useState<NewPasskey | null>(null);
  const [pending, setPending] = useState<"use" | "create" | "confirm" | null>(null);
  const [error, setError] = useState<unknown>(null);

  // callers pass a fresh function on every render; the latest one is used
  // without refetching on each render
  const fetchChallenge = useRef(challenge);
  fetchChallenge.current = challenge;
  const prepare = useCallback(async () => {
    setCurrent(null);
    try {
      setCurrent(await fetchChallenge.current());
    } catch (e) {
      setError(e);
    }
  }, []);

  useEffect(() => {
    if (!open) return;
    setCreated(null);
    setError(null);
    void prepare();
  }, [open, prepare]);

  // answer and verify; a spent or failed challenge is replaced, so the
  // next click has a fresh one
  async function answer(kind: "use" | "confirm", with_?: NewPasskey) {
    if (!current) return;
    setPending(kind);
    setError(null);
    try {
      const a = await answerChallenge(current, with_);
      await verify(a);
    } catch (e) {
      setError(e);
      void prepare();
    } finally {
      setPending(null);
    }
  }

  async function create() {
    if (!current) return;
    setPending("create");
    setError(null);
    try {
      setCreated(await createPasskey(current.rp_id, userName));
    } catch (e) {
      setError(e);
    } finally {
      setPending(null);
    }
  }

  const supported = passkeysAvailable();
  const busy = pending !== null;
  const ready = current !== null && !busy;

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (busy) return;
        onOpenChange(o);
      }}
      title={title}
      description={description}
      size="sm"
    >
      <div className="flex flex-col gap-4" data-testid="passkey-dialog">
        {!supported ? (
          <p className="text-small text-fg-2">
            This browser does not support passkeys. Use a current version of Chrome, Edge, Firefox
            or Safari.
          </p>
        ) : created ? (
          <div className="flex flex-col gap-3">
            <p role="status" className="flex items-start gap-2 text-small text-fg">
              <CircleCheck aria-hidden className="mt-0.5 size-3.5 shrink-0 text-success" />
              Your passkey is created. Confirm it once to finish.
            </p>
            <Button
              type="button"
              className="w-full"
              pending={pending === "confirm"}
              disabled={!ready}
              onClick={() => answer("confirm", created)}
            >
              <KeyRound aria-hidden />
              Confirm with your passkey
            </Button>
          </div>
        ) : (
          <div className="flex flex-col gap-2">
            {mode !== "link" ? (
              <Button
                type="button"
                className="w-full"
                pending={pending === "use"}
                disabled={!ready}
                onClick={() => answer("use")}
              >
                <KeyRound aria-hidden />
                {mode === "confirm" ? "Confirm with your passkey" : "Sign in with your passkey"}
              </Button>
            ) : null}
            {mode !== "confirm" ? (
              <Button
                type="button"
                variant={mode === "sign_in" ? "outline" : "default"}
                className="w-full"
                pending={pending === "create"}
                disabled={!ready}
                onClick={create}
              >
                {mode === "sign_in" ? "Create a new passkey" : "Create a passkey"}
              </Button>
            ) : null}
          </div>
        )}
        {error ? <FormMessage error={error} /> : null}
      </div>
    </Dialog>
  );
}
