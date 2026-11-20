import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { KeyRound, Unlink, Wallet } from "lucide-react";
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
import { RowMenu } from "@/components/row-menu";
import { ErrorState } from "@/components/states";
import { Pill } from "@/components/status";
import { steppedUp } from "@/components/step-up";
import { toast } from "@/components/toast";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
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
  access_denied: "You canceled the link on GitHub. Nothing changed.",
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

const HEADER = { title: "Account" };

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
        <ErrorState
          title="Your account did not load"
          error={me.error}
          onRetry={() => me.refetch()}
        />
      </div>
    );
  return (
    <div className="flex flex-col gap-8">
      <Who me={me.data} />
      <SignInMethods me={me.data} />
      {me.data.user.is_operator ? <OperatorPasskeys userName={me.data.user.display_name} /> : null}
      <DeleteAccount me={me.data} />
    </div>
  );
}

// who you are: the initial, the name the console shows, the projects you own
function Who({ me }: { me: Me }) {
  const owned = me.tenants.filter((t) => t.role === "owner").length;
  return (
    <header className="flex items-center gap-4">
      <span
        aria-hidden
        className="flex size-16 shrink-0 items-center justify-center rounded-full bg-series-1/20 text-[26px] font-semibold text-series-1 uppercase"
      >
        {me.user.display_name.slice(0, 1)}
      </span>
      <div className="flex min-w-0 flex-col gap-1">
        <h1 className="truncate text-[28px] leading-8.5 font-semibold tracking-[-0.02em] text-fg">
          {me.user.display_name}
        </h1>
        <p className="text-small text-fg-2">
          {owned === 0
            ? "Owner of no project"
            : owned === 1
              ? "Owner of 1 project"
              : `Owner of ${owned} projects`}
        </p>
      </div>
    </header>
  );
}

// one sign-in method as a card: its icon, Linked and its menu when linked,
// its name, what identifies it, and its one action when not linked
function MethodCard({
  icon,
  name,
  linked,
  detail,
  action,
  menu,
}: {
  icon: ReactNode;
  name: string;
  linked: boolean;
  detail: ReactNode;
  action?: ReactNode;
  menu?: ReactNode;
}) {
  return (
    <li
      data-method={name}
      data-linked={linked ? "" : undefined}
      className={cn(
        "flex min-h-44 min-w-0 flex-col gap-1 rounded-2xl border bg-raised px-4.5 pt-4.5 pb-5",
        linked ? "border-success-line" : "border-line",
      )}
    >
      <div className="mb-2.5 flex items-start justify-between gap-2">
        <span className="flex size-9.5 shrink-0 items-center justify-center rounded-lg border border-line-2 text-fg-2 [&_svg]:size-4">
          {icon}
        </span>
        {linked ? (
          <span className="flex items-center gap-1">
            <Pill tone="success">Linked</Pill>
            {menu}
          </span>
        ) : null}
      </div>
      <h3 className="text-[15px] leading-5.5 font-semibold text-fg">{name}</h3>
      <div className="flex min-w-0 flex-col gap-1 text-small text-fg-2">{detail}</div>
      {action ? <div className="mt-2.5">{action}</div> : null}
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

// a linked method's one action, unlinking, in its row menu. the only
// method has no menu: nothing can be done with it
function UnlinkMenu({
  method,
  alone,
  onClick,
}: {
  method: Method;
  alone: boolean;
  onClick: () => void;
}) {
  if (alone) return null;
  return (
    <RowMenu
      name={METHOD_NAME[method]}
      actions={[
        { label: "Unlink", icon: <Unlink aria-hidden />, destructive: true, onSelect: onClick },
      ]}
    />
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
  const count = [github_login, stellar_address, passkey_address].filter((m) => m !== null).length;
  const alone = count <= 1;

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
      <h2 id="methods-h" className="flex items-baseline gap-2 text-[15px] font-semibold text-fg">
        Sign-in methods
        <span className="text-small font-normal text-fg-3 tabular">{count} of 3</span>
      </h2>
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
      <ul className="grid grid-cols-1 gap-3 md:grid-cols-3" aria-label="Sign-in methods">
        <MethodCard
          icon={<GitHubMark className="size-4" />}
          name="GitHub"
          linked={github_login !== null}
          detail={
            github_login !== null ? (
              <>
                <span className="truncate">{github_login}</span>
                <span className="font-mono text-caption text-fg-3">id {github_id}</span>
              </>
            ) : (
              <span>Not linked</span>
            )
          }
          menu={
            <UnlinkMenu
              method="github"
              alone={alone}
              onClick={() => {
                unlink.reset();
                setUnlinking("github");
              }}
            />
          }
          action={
            github_login === null ? (
              <Button asChild variant="outline" size="sm" className="max-lg:h-(--vs-control-touch)">
                <a href={linkGitHubUrl()} onClick={clearNotice}>
                  Link GitHub
                </a>
              </Button>
            ) : undefined
          }
        />
        <MethodCard
          icon={<Wallet aria-hidden />}
          name="Stellar wallet"
          linked={stellar_address !== null}
          detail={
            stellar_address !== null ? (
              <span className="font-mono" title={stellar_address}>
                {shortAddress(stellar_address)}
              </span>
            ) : (
              <span>Not linked</span>
            )
          }
          menu={
            <UnlinkMenu
              method="stellar"
              alone={alone}
              onClick={() => {
                unlink.reset();
                setUnlinking("stellar");
              }}
            />
          }
          action={
            stellar_address === null ? (
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
            ) : undefined
          }
        />
        <MethodCard
          icon={<KeyRound aria-hidden />}
          name="Passkey"
          linked={passkey_address !== null}
          detail={
            passkey_address !== null ? (
              <span className="font-mono" title={passkey_address}>
                {shortAddress(passkey_address)}
              </span>
            ) : (
              <span>None yet</span>
            )
          }
          menu={
            <UnlinkMenu
              method="passkey"
              alone={alone}
              onClick={() => {
                unlink.reset();
                setUnlinking("passkey");
              }}
            />
          }
          action={
            passkey_address === null ? (
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
            ) : undefined
          }
        />
      </ul>

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
      <div className="flex flex-col gap-4 border-t border-line-subtle pt-6">
        <div className="flex flex-wrap items-center justify-between gap-4">
          <h2 id="danger-h" className="text-body font-medium text-fg">
            Delete account
          </h2>
          <Button
            variant="outline"
            className="border-critical-line text-critical hover:text-critical max-lg:h-(--vs-control-touch)"
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
        description={
          <div className="flex flex-col gap-2">
            <p>
              The account {me.user.display_name} is deleted and you are signed out everywhere. You
              cannot undo this.
            </p>
            <ul className="flex list-disc flex-col gap-1 pl-5 text-small">
              <li>You leave all your projects.</li>
              <li>Your sessions, AI keys, AI connections and open invites end.</li>
              <li>Your GitHub id, Stellar address and passkey are removed.</li>
              <li>A later sign-in makes a new, empty account.</li>
            </ul>
          </div>
        }
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
