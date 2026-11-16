import { ExternalLink, Wallet } from "lucide-react";
import { type ReactNode, useEffect, useState } from "react";
import { FormMessage } from "@/components/error-panel";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { listWallets, type WalletOption } from "@/lib/stellar-wallet";

// the console's wallet picker, in place of the wallets kit's own modal (which
// the csp refuses, see lib/stellar-wallet). it lists the supported wallets,
// the ones installed in this browser first, and hands the chosen one to
// onPick. onPick does the work (connect, sign, call the control plane) and
// the picker shows its pending state and its error, so every caller gets the
// same behaviour:
//
//   <WalletPicker
//     open={open}
//     onOpenChange={setOpen}
//     title="Continue with a Stellar wallet"
//     purpose="transaction"
//     onPick={async (wallet) => { ... }}
//   />
//
// purpose "message" lists only the wallets that sign sep-53 messages.
export function WalletPicker({
  open,
  onOpenChange,
  title,
  description,
  purpose,
  onPick,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: ReactNode;
  description?: ReactNode;
  purpose: "transaction" | "message";
  onPick: (wallet: WalletOption) => Promise<void>;
}) {
  const [wallets, setWallets] = useState<WalletOption[] | null>(null);
  const [loadError, setLoadError] = useState<unknown>(null);
  const [pending, setPending] = useState<string | null>(null);
  const [error, setError] = useState<unknown>(null);

  useEffect(() => {
    if (!open) return;
    let live = true;
    setError(null);
    setLoadError(null);
    listWallets().then(
      (w) => {
        if (live) setWallets(w);
      },
      (e: unknown) => {
        if (live) setLoadError(e);
      },
    );
    return () => {
      live = false;
    };
  }, [open]);

  const shown = (wallets ?? []).filter((w) => purpose === "transaction" || w.signsMessages);
  const installed = shown.filter((w) => w.available);
  const others = shown.filter((w) => !w.available);

  async function pick(w: WalletOption) {
    setPending(w.id);
    setError(null);
    try {
      await onPick(w);
    } catch (e) {
      setError(e);
    } finally {
      setPending(null);
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (pending) return;
        onOpenChange(o);
      }}
      title={title}
      description={description}
      size="sm"
    >
      <div className="flex flex-col gap-4" data-testid="wallet-picker">
        {loadError ? <FormMessage error={loadError} /> : null}
        {wallets === null && !loadError ? (
          <p className="text-small text-fg-2">Looking for wallets in this browser…</p>
        ) : null}
        {wallets !== null && installed.length === 0 ? (
          <p className="text-small text-fg-2">
            No Stellar wallet extension was found in this browser. Install one of the wallets below,
            unlock it, and open this dialog again.
          </p>
        ) : null}
        {installed.length > 0 ? (
          <ul className="flex flex-col gap-1.5" aria-label="Wallets in this browser">
            {installed.map((w) => (
              <li key={w.id}>
                <Button
                  type="button"
                  variant="outline"
                  className="w-full justify-start"
                  pending={pending === w.id}
                  disabled={pending !== null && pending !== w.id}
                  onClick={() => pick(w)}
                >
                  <Wallet aria-hidden />
                  {w.name}
                </Button>
              </li>
            ))}
          </ul>
        ) : null}
        {error ? <FormMessage error={error} /> : null}
        {others.length > 0 ? (
          <div className="flex flex-col gap-1.5">
            <p className="text-caption text-fg-3">Not installed</p>
            <ul className="flex flex-wrap gap-x-3 gap-y-1">
              {others.map((w) => (
                <li key={w.id}>
                  <a
                    href={w.url}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="inline-flex items-center gap-1 rounded-sm text-small text-fg-2 underline decoration-line-strong underline-offset-2 hover:text-fg focus-ring"
                  >
                    {w.name}
                    <ExternalLink aria-hidden className="size-3" />
                  </a>
                </li>
              ))}
            </ul>
          </div>
        ) : null}
      </div>
    </Dialog>
  );
}
