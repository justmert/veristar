import { useQueryClient } from "@tanstack/react-query";
import { CircleAlert, CircleCheck, FileText, KeyRound, Shield, Wallet } from "lucide-react";
import { useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router";
import { api, signInUrl } from "@/api/endpoints";
import { GitHubMark } from "@/components/github-mark";
import { PasskeyDialog } from "@/components/passkey-dialog";
import { PublicFrame } from "@/components/public-frame";
import { Button } from "@/components/ui/button";
import { WalletPicker } from "@/components/wallet-picker";
import { connectWallet, signTransaction } from "@/lib/stellar-wallet";

// failure codes of the oauth callback (contract 4.1.2)
const SIGN_IN_ERRORS: Record<string, string> = {
  state_mismatch:
    "The sign-in link expired or was opened in another browser. Start again from this page.",
  access_denied: "GitHub sign-in was cancelled. Nothing was shared.",
  github_error: "GitHub refused the sign-in request. Try again in a moment.",
  rate_limited: "Too many sign-ins right now. Wait a few seconds and start again.",
  exchange_failed: "GitHub did not confirm the sign-in. Start again from this page.",
  github_unavailable: "GitHub could not be reached. Try again in a moment.",
  internal:
    "The service could not finish the sign-in. Try again; if it keeps failing, report it to the operators.",
};

// same rule as the control plane (contract 4.1.1): a console path, never a
// scheme-relative or backslash path that a browser would read as another host
export function safeReturnTo(v: string | null): string {
  if (!v || v.length > 512 || !v.startsWith("/") || v.startsWith("//") || v.startsWith("/\\"))
    return "/";
  return v;
}

// wallet sign-in (contract 4.1.7): the wallet signs the control plane's sep-10
// challenge for its account, and the control plane checks it and starts the
// session. it returns the console path to open.
export async function walletSignIn(walletId: string, returnTo: string): Promise<string> {
  const address = await connectWallet(walletId);
  const c = await api.walletChallenge(address, returnTo);
  const signed = await signTransaction(walletId, c.transaction, {
    address,
    networkPassphrase: c.network_passphrase,
  });
  const v = await api.walletVerify(signed);
  return safeReturnTo(v.return_to);
}

export function SignInPage() {
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const [picker, setPicker] = useState(false);
  const [passkey, setPasskey] = useState(false);
  const qc = useQueryClient();
  const code = params.get("error");
  const message = code ? (SIGN_IN_ERRORS[code] ?? SIGN_IN_ERRORS.internal) : null;
  const returnTo = safeReturnTo(params.get("return_to"));
  // set by the account page after DELETE /api/me (contract 4.1.6)
  const deleted = !message && params.get("deleted") === "1";

  return (
    <PublicFrame>
      <div className="flex w-full max-w-[24rem] flex-col gap-6">
        <div className="flex flex-col gap-2">
          <h1 className="text-h1 tracking-tight text-fg">Sign in</h1>
          <p className="text-body text-fg-2">
            Web and API analytics for projects building on Stellar, free for every project.
          </p>
        </div>

        {message ? (
          <p
            role="alert"
            className="flex animate-fade-in items-start gap-2.5 rounded-lg border border-critical-line bg-raised px-3.5 py-3 text-body text-fg"
          >
            <CircleAlert aria-hidden className="mt-0.5 size-4 shrink-0 text-critical" />
            {message}
          </p>
        ) : null}
        {deleted ? (
          <p
            role="status"
            className="flex animate-fade-in items-start gap-2.5 rounded-lg border border-line bg-raised px-3.5 py-3 text-body text-fg"
          >
            <CircleCheck aria-hidden className="mt-0.5 size-4 shrink-0 text-success" />
            Your account was deleted. Signing in again creates a new, empty account.
          </p>
        ) : null}

        <div className="flex flex-col gap-2.5">
          <Button asChild size="lg" className="w-full">
            <a href={signInUrl(returnTo)}>
              <GitHubMark />
              Continue with GitHub
            </a>
          </Button>
          <Button
            type="button"
            variant="outline"
            size="lg"
            className="w-full"
            onClick={() => setPicker(true)}
          >
            <Wallet aria-hidden className="size-4.5" />
            Continue with a Stellar wallet
          </Button>
          <Button
            type="button"
            variant="outline"
            size="lg"
            className="w-full"
            onClick={() => setPasskey(true)}
          >
            <KeyRound aria-hidden className="size-4.5" />
            Continue with a passkey
          </Button>
        </div>

        <WalletPicker
          open={picker}
          onOpenChange={setPicker}
          title="Continue with a Stellar wallet"
          description="Your wallet signs a sign-in challenge for this console on the public network. It moves no funds and sends nothing to the network."
          purpose="transaction"
          onPick={async (wallet) => {
            const dest = await walletSignIn(wallet.id, returnTo);
            // the session changed; nothing cached before it applies
            qc.clear();
            setPicker(false);
            navigate(dest, { replace: true });
          }}
        />

        <PasskeyDialog
          open={passkey}
          onOpenChange={setPasskey}
          mode="sign_in"
          title="Continue with a passkey"
          description="A passkey signs you in with Face ID, Touch ID, Windows Hello or a security key. It is a soropass passkey: its key also controls a Stellar smart account, whose address names your console account."
          userName="Veristar account"
          challenge={() => api.passkeyChallenge(returnTo)}
          verify={async (answer) => {
            const v = await api.passkeyVerify(answer);
            qc.clear();
            setPasskey(false);
            navigate(safeReturnTo(v.return_to), { replace: true });
          }}
        />

        <ul className="flex flex-col gap-2 text-small text-fg-2">
          <li className="flex gap-2">
            <Shield aria-hidden className="mt-0.5 size-3.5 shrink-0 text-fg-3" />
            GitHub shares your public username and id. No email, no repository access.
          </li>
          <li className="flex gap-2">
            <Wallet aria-hidden className="mt-0.5 size-3.5 shrink-0 text-fg-3" />A wallet shares
            your public Stellar address. The signature moves no funds.
          </li>
          <li className="flex gap-2">
            <KeyRound aria-hidden className="mt-0.5 size-3.5 shrink-0 text-fg-3" />A passkey shares
            its public key and its smart account address. Your fingerprint or face never leaves the
            device.
          </li>
          <li className="flex gap-2">
            <FileText aria-hidden className="mt-0.5 size-3.5 shrink-0 text-fg-3" />
            <span>
              Enrolling a project means accepting the{" "}
              <Link
                to="/terms"
                className="rounded-sm text-fg underline decoration-line-strong underline-offset-2 transition-colors duration-(--vs-duration-press) hover:decoration-fg focus-ring"
              >
                terms
              </Link>
              .
            </span>
          </li>
        </ul>
      </div>
    </PublicFrame>
  );
}
