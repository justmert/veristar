import { useMutation, useQuery } from "@tanstack/react-query";
import { CircleCheck, CircleX, Wallet } from "lucide-react";
import { type ReactNode, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router";
import { ApiError } from "@/api/client";
import { api, linkGitHubUrl } from "@/api/endpoints";
import { meQuery } from "@/api/queries";
import type { Me } from "@/api/schemas";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { FormMessage } from "@/components/error-panel";
import { TextField } from "@/components/form-field";
import { GitHubMark } from "@/components/github-mark";
import { PageHeader, Panel } from "@/components/panel";
import { Button } from "@/components/ui/button";
import { WalletPicker } from "@/components/wallet-picker";
import { connectWallet, shortAddress, signTransaction } from "@/lib/stellar-wallet";
import { queryClient } from "@/query-client";

// the typed confirmation of an account deletion: the github login, or the
// whole stellar address of a wallet-only account, exactly, so a stray click
// or a paste of something else cannot delete an account
export function confirmsLogin(typed: string, login: string): boolean {
  return typed.trim() === login;
}

// what the deletion dialog asks to type
export function confirmationText(me: Me): string {
  return me.user.github_login ?? me.user.stellar_address ?? me.user.id;
}

// failure codes of the github link flow (contract 4.1.8), sent back as
// /account?link_error=<code>
const LINK_ERRORS: Record<string, string> = {
  identity_in_use:
    "That GitHub account signs in to another console account. Delete that account, or unlink GitHub there, first.",
  identity_already_linked: "This account already has a GitHub sign-in. Unlink it first.",
  state_mismatch:
    "The link expired, or you signed in to another account meanwhile. Start again from this page.",
  access_denied: "GitHub linking was cancelled. Nothing changed.",
  rate_limited: "Too many requests right now. Wait a few seconds and start again.",
};

// the projects that block a deletion (contract 4.1.6), empty for any other
// outcome
export function blockingProjects(err: unknown): { id: string; name: string }[] {
  return err instanceof ApiError && err.code === "sole_owner" ? err.tenants : [];
}

// the signed-in person's own account: the sign-in methods linked to it
// (contract 4.1.8), and the deletion of the account (contract 4.1.6)
export function AccountPage() {
  const me = useQuery(meQuery);
  if (!me.data) return null;
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Account"
        description="Your console account holds the sign-in methods below, your project memberships and your sessions."
      />
      <SignInMethods me={me.data} />
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
    <li className="flex flex-wrap items-center gap-x-4 gap-y-2 py-3 first:pt-0 last:pb-0">
      <span className="flex size-8 shrink-0 items-center justify-center rounded-md bg-selected text-fg-2 [&_svg]:size-4">
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

function SignInMethods({ me }: { me: Me }) {
  const [params, setParams] = useSearchParams();
  const linkError = params.get("link_error");
  const linked = params.get("linked");
  const [picker, setPicker] = useState(false);
  const [unlinking, setUnlinking] = useState<"github" | "stellar" | null>(null);
  const { github_login, github_id, stellar_address } = me.user;
  const both = github_login !== null && stellar_address !== null;

  const unlink = useMutation({
    mutationFn: api.unlinkIdentity,
    onSuccess: async () => {
      setUnlinking(null);
      await queryClient.invalidateQueries({ queryKey: meQuery.queryKey });
    },
  });

  function clearNotice() {
    if (linkError || linked) setParams({}, { replace: true });
  }

  return (
    <Panel
      title="Linked sign-in methods"
      description="Each method signs in to this same account. Link a second one to keep access if you lose the first; an account always keeps at least one."
    >
      <div className="flex flex-col gap-4">
        {linkError ? (
          <p
            role="alert"
            className="flex items-start gap-2 rounded-md border border-critical-line bg-critical-soft px-2.5 py-2 text-small text-fg"
          >
            <CircleX aria-hidden className="mt-0.5 size-3.5 shrink-0 text-critical" />
            {LINK_ERRORS[linkError] ??
              "Linking GitHub failed on our side. Try again; if it keeps failing, report it."}
          </p>
        ) : null}
        {linked === "github" ? (
          <p
            role="status"
            className="flex items-start gap-2 rounded-md border border-line bg-raised px-2.5 py-2 text-small text-fg"
          >
            <CircleCheck aria-hidden className="mt-0.5 size-3.5 shrink-0 text-success" />
            GitHub is linked. You can sign in with it from now on.
          </p>
        ) : null}
        <ul className="flex flex-col divide-y divide-line" aria-label="Sign-in methods">
          <MethodRow
            icon={<GitHubMark className="size-4" />}
            name="GitHub"
            detail={
              github_login !== null ? (
                <>
                  {github_login} <span className="font-mono tabular-nums">· id {github_id}</span>
                </>
              ) : (
                "Not linked"
              )
            }
            action={
              github_login !== null ? (
                <Button
                  variant="outline"
                  size="sm"
                  disabled={!both}
                  title={both ? undefined : "Link another method before you unlink this one"}
                  onClick={() => {
                    unlink.reset();
                    setUnlinking("github");
                  }}
                >
                  Unlink
                </Button>
              ) : (
                <Button asChild variant="outline" size="sm">
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
                <Button
                  variant="outline"
                  size="sm"
                  disabled={!both}
                  title={both ? undefined : "Link another method before you unlink this one"}
                  onClick={() => {
                    unlink.reset();
                    setUnlinking("stellar");
                  }}
                >
                  Unlink
                </Button>
              ) : (
                <Button
                  variant="outline"
                  size="sm"
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
        </ul>
      </div>

      <WalletPicker
        open={picker}
        onOpenChange={setPicker}
        title="Link a Stellar wallet"
        description="Your wallet signs a sign-in challenge for this console on the public network. It moves no funds and sends nothing to the network."
        purpose="transaction"
        onPick={async (wallet) => {
          const address = await connectWallet(wallet.id);
          const c = await api.linkWalletChallenge(address);
          const signed = await signTransaction(wallet.id, c.transaction, {
            address,
            networkPassphrase: c.network_passphrase,
          });
          await api.linkWallet(signed);
          await queryClient.invalidateQueries({ queryKey: meQuery.queryKey });
          setPicker(false);
        }}
      />

      <ConfirmDialog
        open={unlinking !== null}
        onOpenChange={(o) => {
          if (!o) setUnlinking(null);
        }}
        title={unlinking === "github" ? "Unlink GitHub?" : "Unlink your Stellar wallet?"}
        description={
          unlinking === "github"
            ? "You can no longer sign in to this account with GitHub. Signing in with GitHub afterwards creates a new, empty account."
            : "You can no longer sign in to this account with this wallet. Signing in with it afterwards creates a new, empty account."
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
    </Panel>
  );
}

function DeleteAccount({ me }: { me: Me }) {
  const navigate = useNavigate();
  const login = confirmationText(me);
  const [open, setOpen] = useState(false);
  const [typed, setTyped] = useState("");
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
    <Panel
      title="Delete account"
      description="Deletes your account and signs you out everywhere. This cannot be undone."
      className="border-critical-line"
    >
      <div className="flex flex-col gap-4">
        <ul className="flex list-disc flex-col gap-1 pl-5 text-body text-fg-2">
          <li>
            You leave every project you are a member of, and lose access to its web dashboards.
          </li>
          <li>Your sessions, AI access keys and AI client connections end.</li>
          <li>
            Open invites addressed to your GitHub account or your Stellar address are deleted.
          </li>
          <li>
            Your GitHub id and login and your Stellar address are removed from the account. The
            audit log keeps its entries, which name you by an account id; see the privacy notice for
            what they hold.
          </li>
          <li>Signing in again later, with either method, creates a new, empty account.</li>
        </ul>

        {blocked.length > 0 ? (
          <div
            role="alert"
            className="flex animate-fade-in flex-col gap-2 rounded-md border border-critical-line bg-critical-soft px-3 py-2.5 text-small text-fg"
          >
            <p className="flex items-start gap-2">
              <CircleX aria-hidden className="mt-0.5 size-3.5 shrink-0 text-critical" />
              You are the only owner of{" "}
              {blocked.length === 1 ? "this project" : `these ${blocked.length} projects`}. Make
              another member an owner, or ask the operators to remove the project, then delete your
              account.
            </p>
            <ul className="flex flex-col gap-1 pl-5.5">
              {blocked.map((t) => (
                <li key={t.id}>
                  <Link
                    to={`/projects/${t.id}/members`}
                    className="rounded-sm text-fg underline decoration-line-strong underline-offset-2 transition-colors duration-(--vs-duration-press) hover:decoration-fg focus-ring"
                  >
                    {t.name}
                  </Link>
                </li>
              ))}
            </ul>
          </div>
        ) : null}
        {!open && blocked.length === 0 ? <FormMessage error={del.error} /> : null}

        <div>
          <Button
            variant="destructive"
            onClick={() => {
              setTyped("");
              del.reset();
              setOpen(true);
            }}
          >
            Delete account
          </Button>
        </div>
      </div>

      <ConfirmDialog
        open={open}
        onOpenChange={(o) => {
          setOpen(o);
          if (!o) setTyped("");
        }}
        title="Delete your account?"
        description={`Your account ${me.user.display_name} is deleted and you are signed out everywhere. This cannot be undone.`}
        cancelLabel="Keep my account"
        confirmLabel="Delete account"
        pendingLabel="Deleting"
        pending={del.isPending}
        // a sole_owner refusal is shown on the page with its project links
        error={blocked.length > 0 ? null : del.error}
        confirmDisabled={!confirmsLogin(typed, login)}
        onConfirm={() => del.mutate()}
      >
        <TextField
          label={`Type ${login} to confirm`}
          value={typed}
          onChange={(e) => setTyped(e.target.value)}
          autoComplete="off"
          autoCapitalize="off"
          spellCheck={false}
        />
      </ConfirmDialog>
    </Panel>
  );
}
