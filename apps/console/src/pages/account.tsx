import { useMutation, useQuery } from "@tanstack/react-query";
import { CircleX } from "lucide-react";
import { useState } from "react";
import { Link, useNavigate } from "react-router";
import { ApiError } from "@/api/client";
import { api } from "@/api/endpoints";
import { meQuery } from "@/api/queries";
import type { Me } from "@/api/schemas";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { FormMessage } from "@/components/error-panel";
import { TextField } from "@/components/form-field";
import { PageHeader, Panel } from "@/components/panel";
import { Button } from "@/components/ui/button";
import { queryClient } from "@/query-client";

// the typed confirmation of an account deletion: the github login exactly,
// so a stray click or a paste of something else cannot delete an account
export function confirmsLogin(typed: string, login: string): boolean {
  return typed.trim() === login;
}

// the projects that block a deletion (contract 4.1.6), empty for any other
// outcome
export function blockingProjects(err: unknown): { id: string; name: string }[] {
  return err instanceof ApiError && err.code === "sole_owner" ? err.tenants : [];
}

// the signed-in person's own account: who they are signed in as, and the
// deletion of the account (contract 4.1.6)
export function AccountPage() {
  const me = useQuery(meQuery);
  if (!me.data) return null;
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Account"
        description="Your console account comes from your GitHub sign-in. It holds your GitHub id and login, your project memberships and your sessions."
      />
      <Panel title="Signed in with GitHub">
        <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-6 gap-y-2 text-body">
          <dt className="text-fg-2">Login</dt>
          <dd className="min-w-0 truncate text-fg">{me.data.user.github_login}</dd>
          <dt className="text-fg-2">GitHub id</dt>
          <dd className="font-mono text-fg tabular-nums">{me.data.user.github_id}</dd>
        </dl>
      </Panel>
      <DeleteAccount me={me.data} />
    </div>
  );
}

function DeleteAccount({ me }: { me: Me }) {
  const navigate = useNavigate();
  const login = me.user.github_login;
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
          <li>Open invites addressed to your GitHub account are deleted.</li>
          <li>
            Your GitHub id and login are removed. The audit log keeps its entries, which name you
            only by an account id that no longer leads to you.
          </li>
          <li>Signing in with GitHub again later creates a new, empty account.</li>
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
        description={`Your account ${login} is deleted and you are signed out everywhere. This cannot be undone.`}
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
