import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { KeyRound, Wallet } from "lucide-react";
import { type ReactNode, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router";
import { ApiError } from "@/api/client";
import { api, linkGitHubUrl } from "@/api/endpoints";
import { operatorsApi, stepUpQuery } from "@/api/operators";
import { meQuery } from "@/api/queries";
import type { Me } from "@/api/schemas";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { FormMessage } from "@/components/error-panel";
import { GitHubMark } from "@/components/github-mark";
import { Notice } from "@/components/notice";
import { OperatorPasskeys } from "@/components/operator-passkeys";
import { PageHeader } from "@/components/page-header";
import { PasskeyDialog } from "@/components/passkey-dialog";
import { ErrorState } from "@/components/states";
import { steppedUp } from "@/components/step-up";
import { toast } from "@/components/toast";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Tooltip } from "@/components/ui/tooltip";
import { WalletPicker } from "@/components/wallet-picker";
import { connectWallet, shortAddress, signTransaction } from "@/lib/stellar-wallet";
import { queryClient } from "@/query-client";

// an operator account confirms identity changes with its operator passkey
// first (contract 5.15); the control plane answers step_up_required until it
// has a fresh confirmation
function needsStepUp(err: unknown): boolean {
  return err instanceof ApiError && err.code === "step_up_required";
}

// what the deletion dialog asks to type
export function confirmationText(me: Me): string {
  return me.user.github_login ?? me.user.stellar_address ?? me.user.passkey_address ?? me.user.id;
}

// failure codes of the github link flow (contract 4.1.8), sent back as
// /account?link_error=<code>
export const LINK_ERRORS: Record<string, string> = {
  identity_in_use:
    "That GitHub account signs in to another console account. Delete that account, or unlink GitHub there, first.",
  identity_already_linked: "This account already has a GitHub sign-in. Unlink it first.",
  state_mismatch:
    "The link expired, or you signed in to another account meanwhile. Start again from this page.",
  access_denied: "You cancelled the link on GitHub. Nothing changed.",
  rate_limited: "Too many requests. Wait a few seconds, then start again.",
  step_up_required:
    "This is an operator account. Confirm with your passkey on this page, then link GitHub again.",
};

export const LINK_ERROR_OTHER =
  "GitHub was not linked. Try again. If it fails again, tell the operators.";

// the projects that block a deletion (contract 4.1.6), empty for any other
// outcome
export function blockingProjects(err: unknown): { id: string; name: string }[] {
  return err instanceof ApiError && err.code === "sole_owner" ? err.tenants : [];
}

type Method = "github" | "stellar" | "passkey";

const METHOD_NAME: Record<Method, string> = {
  github: "GitHub",
  stellar: "Your Stellar wallet",
  passkey: "Your passkey",
};

const HEADER = {
  title: "Account",
  description: "How you sign in to the console. Operators also manage their passkeys here.",
};

// the signed-in person's own account: the sign-in methods linked to it
// (contract 4.1.8), an operator's passkeys (5.15), and the deletion of the
// account (4.1.6)
export function AccountPage() {
  const me = useQuery(meQuery);
  if (me.isPending)
    return (
      <div className="flex flex-col gap-8">
        <PageHeader {...HEADER} />
        <div role="status" aria-busy="true" className="flex max-w-prose flex-col gap-3">
          <span className="sr-only">Loading your account</span>
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} className="h-(--vs-row) w-full" />
          ))}
        </div>
      </div>
    );
  if (me.error)
    return (
      <div className="flex flex-col gap-8">
        <PageHeader {...HEADER} />
        <ErrorState error={me.error} onRetry={() => me.refetch()} />
      </div>
    );
  return (
    <div className="flex max-w-prose flex-col gap-8">
      <PageHeader {...HEADER} />
      <SignInMethods me={me.data} />
      {me.data.user.is_operator ? <OperatorPasskeys userName={me.data.user.display_name} /> : null}
      <DeleteAccount me={me.data} />
    </div>
  );
}

function MethodRow({
  icon,
  name,
  detail,
  action,
}: {
  icon: ReactNode;
  name: string;
  detail: ReactNode;
  action: ReactNode;
}) {
  return (
    <li className="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-line-subtle px-4 py-3 last:border-b-0">
      <span className="flex size-8 shrink-0 items-center justify-center rounded-md border border-line-strong text-fg-2 [&_svg]:size-4">
        {icon}
      </span>
      <div className="flex min-w-0 flex-1 flex-col">
        <span className="text-body font-medium text-fg">{name}</span>
        <span className="min-w-0 truncate text-small text-fg-2">{detail}</span>
      </div>
      {action}
    </li>
  );
}

// an operator account changes its sign-in methods only after a passkey
// step-up (contract 4.1.8): a method added from a stolen session would sign
// in on its own. the control plane refuses the change without one; this
// notice is how the person gives it.
function OperatorStepUp() {
  const s = useQuery(stepUpQuery);
  const [open, setOpen] = useState(false);
  if (!s.data || s.data.stepped_up) return null;
  if (s.data.passkeys === 0)
    return (
      <Notice
        tone="warning"
        title="Add a passkey for the operator area first"
        actions={
          <Button asChild variant="outline" size="sm">
            <Link to="/operator">Open the operator area</Link>
          </Button>
        }
      >
        This is an operator account. Its sign-in methods change only after you confirm with a
        passkey.
      </Notice>
    );
  return (
    <Notice
      tone="info"
      title="Confirm with your passkey to change sign-in methods"
      actions={
        <Button variant="outline" size="sm" onClick={() => setOpen(true)}>
          Use your passkey
        </Button>
      }
    >
      This is an operator account. Links and unlinks need a recent passkey confirmation.
      <PasskeyDialog
        open={open}
        onOpenChange={setOpen}
        mode="confirm"
        title="Confirm with your passkey"
        userName=""
        challenge={() => operatorsApi.stepUpChallenge()}
        verify={async (answer) => {
          const st = await operatorsApi.stepUp(answer);
          await steppedUp(st);
          setOpen(false);
        }}
      />
    </Notice>
  );
}

function UnlinkButton({
  method,
  alone,
  onClick,
}: {
  method: Method;
  alone: boolean;
  onClick: () => void;
}) {
  const button = (
    <Button
      variant="outline"
      size="sm"
      disabled={alone}
      aria-describedby={alone ? "unlink-note" : undefined}
      data-method={method}
      onClick={onClick}
      className="max-lg:h-(--vs-control-touch)"
    >
      Unlink
    </Button>
  );
  if (!alone) return button;
  return (
    <Tooltip content="Link another method first.">
      <span className="inline-flex">{button}</span>
    </Tooltip>
  );
}

function SignInMethods({ me }: { me: Me }) {
  const [params, setParams] = useSearchParams();
  const linkError = params.get("link_error");
  const linked = params.get("linked");
  const [picker, setPicker] = useState(false);
  const [passkey, setPasskey] = useState(false);
  const [unlinking, setUnlinking] = useState<Method | null>(null);
  // the provider's client, so the refreshed account shows wherever it is read
  const qc = useQueryClient();
  const { github_login, github_id, stellar_address, passkey_address } = me.user;
  // a method can go while another one remains
  const alone =
    [github_login, stellar_address, passkey_address].filter((m) => m !== null).length <= 1;

  // the change that waits for a passkey confirmation, if any
  const [stepUp, setStepUp] = useState<"unlink" | "link" | null>(null);

  const unlink = useMutation({
    mutationFn: api.unlinkIdentity,
    onSuccess: async (_, method) => {
      setUnlinking(null);
      toast.success(`${METHOD_NAME[method]} is unlinked.`);
      await qc.invalidateQueries({ queryKey: meQuery.queryKey });
    },
    onError: (err) => {
      if (needsStepUp(err)) setStepUp("unlink");
    },
  });

  function clearNotice() {
    if (linkError || linked) setParams({}, { replace: true });
  }

  return (
    <section aria-labelledby="methods-h" className="flex flex-col gap-4">
      <div className="flex flex-col gap-1">
        <h2 id="methods-h" className="text-title text-fg">
          Sign-in methods
        </h2>
        <p className="max-w-prose text-small text-fg-2">
          Each method signs in to this account. Link a second method, so you keep access if you lose
          one.
        </p>
      </div>
      {linkError ? (
        <Notice tone="critical" role="alert" title="GitHub was not linked">
          {LINK_ERRORS[linkError] ?? LINK_ERROR_OTHER}
        </Notice>
      ) : null}
      {linked === "github" ? (
        <Notice tone="success" title="GitHub is linked.">
          You can sign in with it now.
        </Notice>
      ) : null}
      {me.user.is_operator ? <OperatorStepUp /> : null}
      <ul
        className="flex flex-col rounded-lg border border-line bg-raised"
        aria-label="Sign-in methods"
      >
        <MethodRow
          icon={<GitHubMark className="size-4" />}
          name="GitHub"
          detail={
            github_login !== null ? (
              <>
                {github_login} <span className="font-mono tabular">· id {github_id}</span>
              </>
            ) : (
              "Not linked"
            )
          }
          action={
            github_login !== null ? (
              <UnlinkButton
                method="github"
                alone={alone}
                onClick={() => {
                  unlink.reset();
                  setUnlinking("github");
                }}
              />
            ) : (
              <Button asChild variant="outline" size="sm" className="max-lg:h-(--vs-control-touch)">
                <a href={linkGitHubUrl()} onClick={clearNotice}>
                  Link GitHub
                </a>
              </Button>
            )
          }
        />
        <MethodRow
          icon={<Wallet aria-hidden />}
          name="Stellar wallet"
          detail={
            stellar_address !== null ? (
              <span className="font-mono" title={stellar_address}>
                {shortAddress(stellar_address)}
              </span>
            ) : (
              "Not linked"
            )
          }
          action={
            stellar_address !== null ? (
              <UnlinkButton
                method="stellar"
                alone={alone}
                onClick={() => {
                  unlink.reset();
                  setUnlinking("stellar");
                }}
              />
            ) : (
              <Button
                variant="outline"
                size="sm"
                className="max-lg:h-(--vs-control-touch)"
                onClick={() => {
                  clearNotice();
                  setPicker(true);
                }}
              >
                Link a wallet
              </Button>
            )
          }
        />
        <MethodRow
          icon={<KeyRound aria-hidden />}
          name="Passkey"
          detail={
            passkey_address !== null ? (
              <span className="font-mono" title={passkey_address}>
                {shortAddress(passkey_address)}
              </span>
            ) : (
              "Not linked"
            )
          }
          action={
            passkey_address !== null ? (
              <UnlinkButton
                method="passkey"
                alone={alone}
                onClick={() => {
                  unlink.reset();
                  setUnlinking("passkey");
                }}
              />
            ) : (
              <Button
                variant="outline"
                size="sm"
                className="max-lg:h-(--vs-control-touch)"
                onClick={() => {
                  clearNotice();
                  setPasskey(true);
                }}
              >
                Add a passkey
              </Button>
            )
          }
        />
      </ul>
      {alone ? (
        <p id="unlink-note" className="text-small text-fg-2">
          Unlink is off while this is your only sign-in method. Link another method first.
        </p>
      ) : null}

      <PasskeyDialog
        open={passkey}
        onOpenChange={setPasskey}
        mode="link"
        title="Add a passkey"
        description="A passkey signs you in with Face ID, Touch ID, Windows Hello or a security key."
        userName={me.user.display_name}
        challenge={api.linkPasskeyChallenge}
        verify={async (answer) => {
          try {
            await api.linkPasskey(answer);
          } catch (err) {
            // the created passkey stays in this dialog; after the
            // confirmation its button links it with a fresh challenge
            if (needsStepUp(err)) setStepUp("link");
            throw err;
          }
          await qc.invalidateQueries({ queryKey: meQuery.queryKey });
          setPasskey(false);
          toast.success("Your passkey is linked.");
        }}
      />

      <PasskeyDialog
        open={stepUp !== null}
        onOpenChange={(o) => {
          if (!o) setStepUp(null);
        }}
        mode="confirm"
        title="Confirm it is you"
        description="This account has operator access. A change to its sign-in methods needs your operator passkey."
        userName=""
        challenge={() => operatorsApi.stepUpChallenge()}
        verify={async (answer) => {
          const s = await operatorsApi.stepUp(answer);
          await steppedUp(s);
          const waiting = stepUp;
          setStepUp(null);
          if (waiting === "unlink" && unlinking) unlink.mutate(unlinking);
        }}
      />

      <WalletPicker
        open={picker}
        onOpenChange={setPicker}
        title="Link a Stellar wallet"
        description="Your wallet signs a sign-in challenge for this console. It moves no funds and sends nothing to the network."
        purpose="transaction"
        onPick={async (wallet) => {
          const address = await connectWallet(wallet.id);
          const c = await api.linkWalletChallenge(address);
          const signed = await signTransaction(wallet.id, c.transaction, {
            address,
            networkPassphrase: c.network_passphrase,
          });
          await api.linkWallet(signed);
          await qc.invalidateQueries({ queryKey: meQuery.queryKey });
          setPicker(false);
          toast.success("Your wallet is linked.");
        }}
      />

      <ConfirmDialog
        open={unlinking !== null}
        onOpenChange={(o) => {
          if (!o) setUnlinking(null);
        }}
        title={
          unlinking === "github"
            ? "Unlink GitHub?"
            : unlinking === "passkey"
              ? "Unlink your passkey?"
              : "Unlink your Stellar wallet?"
        }
        description={
          unlinking === "github"
            ? "You can no longer sign in to this account with GitHub. A later GitHub sign-in makes a new, empty account."
            : unlinking === "passkey"
              ? "You can no longer sign in to this account with this passkey. The passkey stays on your device until you delete it there."
              : "You can no longer sign in to this account with this wallet. A later sign-in with it makes a new, empty account."
        }
        cancelLabel="Keep it"
        confirmLabel="Unlink"
        pendingLabel="Unlinking"
        pending={unlink.isPending}
        error={unlink.error}
        onConfirm={() => {
          if (unlinking) unlink.mutate(unlinking);
        }}
      />
    </section>
  );
}

function DeleteAccount({ me }: { me: Me }) {
  const navigate = useNavigate();
  const login = confirmationText(me);
  const [open, setOpen] = useState(false);
  const del = useMutation({
    mutationFn: api.deleteAccount,
    onSuccess: () => {
      queryClient.clear();
      navigate("/sign-in?deleted=1", { replace: true });
    },
    onError: (err) => {
      // the blocking projects are listed on the page, where their links work
      if (blockingProjects(err).length > 0) setOpen(false);
    },
  });
  const blocked = blockingProjects(del.error);

  return (
    <section aria-labelledby="danger-h" className="flex flex-col gap-4">
      <h2 id="danger-h" className="text-title text-fg">
        Danger zone
      </h2>
      <div className="flex flex-col gap-4 rounded-lg border border-critical-line bg-raised p-5 max-sm:p-4">
        <div className="flex flex-wrap items-start justify-between gap-4">
          <div className="flex min-w-0 max-w-prose flex-col gap-2">
            <span className="text-body font-medium text-fg">Delete account</span>
            <ul className="flex list-disc flex-col gap-1 pl-5 text-small text-fg-2">
              <li>You leave all your projects and lose access to their dashboards.</li>
              <li>Your sessions, AI access keys and AI connections end.</li>
              <li>Open invites to your GitHub account, wallet or passkey are deleted.</li>
              <li>
                Your GitHub id, Stellar address and passkey are removed. The audit log keeps its
                entries, which name only your account id.
              </li>
              <li>A later sign-in with any method makes a new, empty account.</li>
            </ul>
          </div>
          <Button
            variant="destructive"
            className="max-lg:h-(--vs-control-touch)"
            onClick={() => {
              del.reset();
              setOpen(true);
            }}
          >
            Delete account
          </Button>
        </div>

        {blocked.length > 0 ? (
          <Notice tone="critical" role="alert" title="You are the only owner">
            <p>
              Make another member an owner of{" "}
              {blocked.length === 1 ? "this project" : `these ${blocked.length} projects`}, or ask
              the operators to remove it. Then delete your account.
            </p>
            <ul className="mt-2 flex flex-col gap-1 pl-5">
              {blocked.map((t) => (
                <li key={t.id} className="list-disc">
                  <Link
                    to={`/projects/${t.id}/members`}
                    className="rounded-sm text-fg underline decoration-line-strong underline-offset-2 transition-colors duration-(--vs-duration-press) hover:decoration-fg focus-ring"
                  >
                    {t.name}
                  </Link>
                </li>
              ))}
            </ul>
          </Notice>
        ) : null}
        {!open && blocked.length === 0 ? <FormMessage error={del.error} /> : null}
      </div>

      <ConfirmDialog
        open={open}
        onOpenChange={setOpen}
        title="Delete your account?"
        description={`The account ${me.user.display_name} is deleted and you are signed out everywhere. You cannot undo this.`}
        cancelLabel="Keep my account"
        confirmLabel="Delete account"
        pendingLabel="Deleting"
        pending={del.isPending}
        // a sole_owner refusal is shown on the page with its project links
        error={blocked.length > 0 ? null : del.error}
        typed={{ value: login, label: `Type ${login} to confirm` }}
        onConfirm={() => del.mutate()}
      />
    </section>
  );
}
