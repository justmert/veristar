import { useQueryClient } from "@tanstack/react-query";
import { KeyRound, LoaderCircle, Lock, ShieldCheck, Wallet } from "lucide-react";
import { Fragment, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router";
import { api, signInUrl } from "@/api/endpoints";
import { GitHubMark } from "@/components/github-mark";
import { Notice } from "@/components/notice";
import { PasskeyDialog } from "@/components/passkey-dialog";
import { BrandLink, PublicFooter } from "@/components/public-frame";
import { Button } from "@/components/ui/button";
import { WalletPicker } from "@/components/wallet-picker";
import { connectWallet, signTransaction } from "@/lib/stellar-wallet";

// failure codes of the oauth callback (contract 4.1.2)
const SIGN_IN_ERRORS: Record<string, string> = {
  state_mismatch:
    "The sign-in link expired or was opened in another browser. Start again from this page.",
  access_denied: "GitHub sign-in was canceled. Nothing was shared.",
  github_error: "GitHub refused the sign-in request. Try again in a moment.",
  rate_limited: "Too many sign-ins right now. Wait a few seconds and start again.",
  exchange_failed: "GitHub did not confirm the sign-in. Start again from this page.",
  github_unavailable: "GitHub could not be reached. Try again in a moment.",
  internal:
    "The service could not finish the sign-in. Try again. If it keeps failing, check the service status.",
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
  // the github sign-in is a full page navigation that can take seconds
  const [leaving, setLeaving] = useState(false);
  const qc = useQueryClient();
  const code = params.get("error");
  const message = code ? (SIGN_IN_ERRORS[code] ?? SIGN_IN_ERRORS.internal) : null;
  const returnTo = safeReturnTo(params.get("return_to"));
  // set by the account page after DELETE /api/me (contract 4.1.6)
  const deleted = !message && params.get("deleted") === "1";

  return (
    // two halves: the darker left half runs the full height with the brand at
    // its top, the footer sits under the right half only
    <div className="flex min-h-dvh flex-col bg-ground text-fg lg:flex-row">
      <SignedProof />
      <div className="flex flex-1 flex-col">
        <main id="main" className="flex flex-1 animate-rise-in px-4 py-10 sm:px-8">
          <section
            aria-labelledby="sign-in-h"
            className="m-auto flex w-full max-w-90 flex-col gap-6"
          >
            <div className="flex flex-col gap-2">
              <h1 id="sign-in-h" className="text-heading text-fg">
                Sign in
              </h1>
              <p className="text-body text-fg-2">Free for every Stellar project</p>
            </div>

            {message ? (
              <Notice tone="critical" role="alert">
                {message}
              </Notice>
            ) : null}
            {deleted ? (
              <Notice tone="success">
                Your account is deleted. A new sign-in makes a new, empty account.
              </Notice>
            ) : null}

            <div className="flex flex-col gap-2.5">
              <Button asChild size="lg" className="h-11.5 w-full" pending={leaving}>
                <a href={signInUrl(returnTo)} onClick={() => setLeaving(true)}>
                  {leaving ? <LoaderCircle aria-hidden className="animate-spin" /> : <GitHubMark />}
                  Continue with GitHub
                </a>
              </Button>
              <Button
                type="button"
                variant="outline"
                size="lg"
                className="h-11.5 w-full"
                onClick={() => setPicker(true)}
              >
                <Wallet aria-hidden className="size-4.5" />
                Continue with a Stellar wallet
              </Button>
              <Button
                type="button"
                variant="outline"
                size="lg"
                className="h-11.5 w-full"
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
              description="A passkey signs you in with Face ID, Touch ID, Windows Hello or a security key. The passkey also controls a Stellar smart account. Its address is the name of your account."
              userName="Veristar account"
              challenge={() => api.passkeyChallenge(returnTo)}
              verify={async (answer) => {
                const v = await api.passkeyVerify(answer);
                qc.clear();
                setPasskey(false);
                navigate(safeReturnTo(v.return_to), { replace: true });
              }}
            />

            <p className="text-small text-fg-2">
              GitHub shares your username and id, a wallet its public address, a passkey a public
              key. Nothing else. Enrolling a project means accepting the{" "}
              <Link
                to="/terms"
                className="rounded-sm text-fg-2 underline decoration-line-strong underline-offset-3 transition-colors duration-(--vs-duration-press) hover:text-fg hover:decoration-fg focus-ring"
              >
                terms
              </Link>
              .
            </p>
          </section>
        </main>
        <PublicFooter tagline="No fees, no ads, no cookies" />
      </div>
    </div>
  );
}

// the left half: the promise and the product itself, a signed report file
// with a stamp across it. an illustration of the file format, so it is
// hidden from assistive technology except for its label
function SignedProof() {
  const lines: [string, string, "s" | "n"][] = [
    ["project", '"Lumen Pay"', "s"],
    ["period", '"2026-10-01 to 2026-10-31"', "s"],
    ["verified_visitors", "1284", "n"],
    ["api_requests", "2410000", "n"],
    ["onchain_operations", "12450", "n"],
    ["integrity_grade", '"B"', "s"],
  ];
  return (
    <aside
      aria-label="What a signed report looks like"
      className="relative flex flex-col overflow-hidden bg-sunken px-4 pt-0 pb-10 shadow-[1px_0_0_var(--color-line-subtle)] sm:px-14 lg:w-[54%] lg:pb-11"
    >
      <div className="flex h-16 items-center">
        <BrandLink />
      </div>
      <div className="my-auto flex flex-col gap-8 pt-10 lg:gap-9 lg:pt-0">
        <p className="max-w-120 text-[clamp(1.75rem,3vw,2.25rem)] leading-[1.17] font-semibold tracking-[-0.03em] text-fg">
          Proof of real usage, signed and checkable
        </p>
        <figure
          aria-hidden
          className="relative w-full max-w-125 overflow-hidden rounded-xl border border-line bg-raised shadow-[0_30px_80px_var(--color-shadow-strong)]"
        >
          <div className="flex items-center gap-2 px-3.5 py-2.5 font-mono text-[11.5px] text-fg-2 shadow-[0_1px_0_var(--color-line-subtle)]">
            <Lock className="size-3" />
            lumen-pay-2026-10.json
          </div>
          <div className="grid grid-cols-[28px_minmax(0,1fr)] pt-3.5 pb-4.5 font-mono text-[12.5px] leading-5.25">
            <span className="pr-3 text-right text-fg-3 select-none">1</span>
            <span className="text-fg-3">{"{"}</span>
            {lines.map(([k, v, kind], i) => (
              <Fragment key={k}>
                <span className="pr-3 text-right text-fg-3 select-none">{i + 2}</span>
                <span className="truncate whitespace-pre">
                  {"  "}
                  <span className="text-fg-2">"{k}"</span>
                  <span className="text-fg-3">: </span>
                  <span className={kind === "s" ? "text-success" : "text-info"}>{v}</span>
                  {i < lines.length - 1 ? <span className="text-fg-3">,</span> : null}
                </span>
              </Fragment>
            ))}
            <span className="pr-3 text-right text-fg-3 select-none">{lines.length + 2}</span>
            <span className="text-fg-3">{"}"}</span>
          </div>
          <div className="flex items-center gap-2.5 px-4 py-3 text-[12.5px] text-fg-2 shadow-[0_-1px_0_var(--color-line-subtle)]">
            <ShieldCheck className="size-4 text-success" />
            <span>Signature valid</span>
            <span className="ml-auto font-mono text-[11.5px] text-fg-3">sig 3f9a51…c07e</span>
          </div>
          <Stamp />
        </figure>
      </div>
    </aside>
  );
}

function Stamp() {
  return (
    <svg
      aria-hidden="true"
      viewBox="0 0 160 160"
      className="absolute -right-7 bottom-14.5 size-39 -rotate-12 text-success max-sm:hidden"
    >
      <defs>
        <path id="stamp-arc" d="M22,80 a58,58 0 1,1 116,0 a58,58 0 1,1 -116,0" />
      </defs>
      <circle
        cx="80"
        cy="80"
        r="74"
        fill="none"
        stroke="currentColor"
        strokeOpacity=".55"
        strokeWidth="2"
      />
      <circle
        cx="80"
        cy="80"
        r="44"
        fill="none"
        stroke="currentColor"
        strokeOpacity=".45"
        strokeWidth="1.5"
      />
      <text
        fontSize="11.5"
        fontWeight="600"
        letterSpacing="3"
        fill="currentColor"
        fillOpacity=".75"
      >
        <textPath href="#stamp-arc">SIGNED BY VERISTAR AND CHECKABLE ANYWHERE</textPath>
      </text>
      <text
        x="80"
        y="88"
        textAnchor="middle"
        fontSize="26"
        fontWeight="700"
        fill="currentColor"
        fillOpacity=".85"
      >
        B
      </text>
    </svg>
  );
}
