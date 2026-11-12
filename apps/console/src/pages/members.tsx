import { useMutation, useQuery } from "@tanstack/react-query";
import { UserPlus } from "lucide-react";
import { type FormEvent, useState } from "react";
import { useNavigate } from "react-router";
import { ApiError } from "@/api/client";
import { api } from "@/api/endpoints";
import { keys, membersQuery } from "@/api/queries";
import type { AddMemberResult, Invite, Member, Role } from "@/api/schemas";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { ErrorPanel, FormMessage, Loading } from "@/components/error-panel";
import { NativeSelect, SelectField, TextField } from "@/components/form-field";
import { Button } from "@/components/ui/button";
import { formatDate } from "@/lib/format";
import * as v from "@/lib/validate";
import { queryClient } from "@/query-client";
import { useProject } from "./project-layout";

export type AddMemberValues = { login: string; role: Role };
export type AddMemberErrors = Partial<Record<keyof AddMemberValues, string>>;

export function validateAddMember(values: AddMemberValues): AddMemberErrors {
  const code = v.githubLogin(values.login.trim().replace(/^@/, ""));
  return code ? { login: v.fieldMessage(code) } : {};
}

export function serverMemberErrors(err: unknown): AddMemberErrors {
  if (!(err instanceof ApiError)) return {};
  if (err.code === "already_member") return { login: "Already a member of this project." };
  if (err.code === "invite_exists")
    return { login: "Already invited. The invite is listed below." };
  if (err.code !== "validation_failed") return {};
  const out: AddMemberErrors = {};
  for (const [path, code] of Object.entries(err.fields)) {
    if (path === "github_login") out.login = v.fieldMessage(code);
    else if (path === "role") out.role = v.fieldMessage(code);
    // too_many is reported on the body as a whole
    else out.login ??= v.fieldMessage(code);
  }
  return out;
}

export function addResultText(r: AddMemberResult): string {
  if (r.kind === "member") return `${r.member.github_login} was added as ${r.member.role}.`;
  return `${r.invite.github_login} has no account yet. They join as ${r.invite.role} when they first sign in, until ${formatDate(r.invite.expires_at)}.`;
}

export function MembersPage() {
  const { tenant, me, isOwner, termsCurrent } = useProject();
  const list = useQuery(membersQuery(tenant.id));

  if (list.isPending) return <Loading label="Loading members" />;
  if (list.error) return <ErrorPanel error={list.error} onRetry={() => list.refetch()} />;
  const ownerCount = list.data.members.filter((m) => m.role === "owner").length;

  return (
    <div className="space-y-8">
      <section className="space-y-4">
        <h2 className="text-xl font-semibold tracking-tight">Members</h2>
        <p className="max-w-prose text-sm text-muted-foreground">
          Members manage sites and open the web dashboards. Owners also add and remove people and
          delete sites.
        </p>
        {isOwner && termsCurrent ? <AddMemberForm tenantId={tenant.id} /> : null}
        <ul className="divide-y overflow-hidden rounded-xl border bg-card">
          {list.data.members.map((m) => (
            <li key={m.user_id}>
              <MemberRow
                tenantId={tenant.id}
                member={m}
                isSelf={m.user_id === me.user.id}
                canManage={isOwner && termsCurrent}
                lastOwner={m.role === "owner" && ownerCount === 1}
              />
            </li>
          ))}
        </ul>
      </section>

      {list.data.invites.length > 0 ? (
        <section className="space-y-4">
          <h2 className="text-lg font-semibold tracking-tight">Open invites</h2>
          <ul className="divide-y overflow-hidden rounded-xl border bg-card">
            {list.data.invites.map((i) => (
              <li key={i.id}>
                <InviteRow tenantId={tenant.id} invite={i} canManage={isOwner && termsCurrent} />
              </li>
            ))}
          </ul>
        </section>
      ) : null}
    </div>
  );
}

export function AddMemberForm({ tenantId }: { tenantId: string }) {
  const [values, setValues] = useState<AddMemberValues>({ login: "", role: "member" });
  const [errors, setErrors] = useState<AddMemberErrors>({});
  const [done, setDone] = useState<string | null>(null);
  const add = useMutation({
    mutationFn: (body: { github_login: string; role: Role }) => api.addMember(tenantId, body),
    onSuccess: async (r) => {
      setDone(addResultText(r));
      setValues({ login: "", role: "member" });
      await queryClient.invalidateQueries({ queryKey: keys.members(tenantId) });
    },
    onError: (err) => setErrors(serverMemberErrors(err)),
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    setDone(null);
    const found = validateAddMember(values);
    setErrors(found);
    if (Object.keys(found).length > 0) return;
    add.mutate({ github_login: values.login.trim().replace(/^@/, ""), role: values.role });
  }

  return (
    <form
      noValidate
      onSubmit={submit}
      aria-label="Add a member"
      className="rounded-xl border bg-card p-4 sm:p-5"
    >
      <div className="grid gap-4 sm:grid-cols-[1fr_10rem_auto] sm:items-start">
        <TextField
          label="GitHub username"
          name="github_login"
          autoCapitalize="none"
          autoComplete="off"
          spellCheck={false}
          maxLength={40}
          value={values.login}
          onChange={(e) => setValues((s) => ({ ...s, login: e.target.value }))}
          error={errors.login}
        />
        <SelectField
          label="Role"
          name="role"
          value={values.role}
          onChange={(e) => setValues((s) => ({ ...s, role: e.target.value as Role }))}
          error={errors.role}
        >
          <option value="member">Member</option>
          <option value="owner">Owner</option>
        </SelectField>
        <Button type="submit" className="sm:mt-6" disabled={add.isPending}>
          <UserPlus aria-hidden />
          {add.isPending ? "Adding" : "Add"}
        </Button>
      </div>
      <div className="mt-3">
        {done ? (
          <p role="status" className="text-sm text-good">
            {done}
          </p>
        ) : null}
        <FormMessage
          error={
            add.error && Object.keys(serverMemberErrors(add.error)).length === 0 ? add.error : null
          }
        />
      </div>
    </form>
  );
}

function MemberRow({
  tenantId,
  member,
  isSelf,
  canManage,
  lastOwner,
}: {
  tenantId: string;
  member: Member;
  isSelf: boolean;
  canManage: boolean;
  lastOwner: boolean;
}) {
  const navigate = useNavigate();
  const setRole = useMutation({
    mutationFn: (role: Role) => api.setMemberRole(tenantId, member.user_id, role),
    onSettled: async () => {
      await queryClient.invalidateQueries({ queryKey: keys.members(tenantId) });
      if (isSelf) {
        await queryClient.invalidateQueries({ queryKey: keys.tenant(tenantId) });
        await queryClient.invalidateQueries({ queryKey: keys.me });
      }
    },
  });
  const remove = useMutation({
    mutationFn: () => api.removeMember(tenantId, member.user_id),
    onSuccess: async () => {
      if (isSelf) {
        // after leaving, every route of this project answers 404
        queryClient.removeQueries({ queryKey: keys.tenant(tenantId) });
        await queryClient.invalidateQueries({ queryKey: keys.me });
        navigate("/", { replace: true });
        return;
      }
      await queryClient.invalidateQueries({ queryKey: keys.members(tenantId) });
    },
  });

  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-2 p-4 sm:px-5">
      <div className="min-w-0 flex-1">
        <p className="truncate font-medium">
          <a
            href={`https://github.com/${encodeURIComponent(member.github_login)}`}
            rel="noopener noreferrer"
            className="hover:underline"
          >
            {member.github_login}
          </a>
          {isSelf ? <span className="font-normal text-muted-foreground"> (you)</span> : null}
        </p>
        <p className="text-sm text-muted-foreground">Since {formatDate(member.created_at)}</p>
      </div>
      {canManage ? (
        <NativeSelect
          aria-label={`Role of ${member.github_login}`}
          value={member.role}
          disabled={setRole.isPending || lastOwner}
          title={lastOwner ? "The last owner cannot be made a member" : undefined}
          onChange={(e) => setRole.mutate(e.target.value as Role)}
        >
          <option value="member">Member</option>
          <option value="owner">Owner</option>
        </NativeSelect>
      ) : (
        <span className="text-sm text-muted-foreground">
          {member.role === "owner" ? "Owner" : "Member"}
        </span>
      )}
      {(canManage || isSelf) && !lastOwner ? (
        <RemoveMember
          login={member.github_login}
          isSelf={isSelf}
          pending={remove.isPending}
          error={remove.error}
          onConfirm={() => remove.mutate()}
        />
      ) : null}
      {setRole.error ? (
        <div className="basis-full">
          <FormMessage error={setRole.error} />
        </div>
      ) : null}
    </div>
  );
}

function RemoveMember({
  login,
  isSelf,
  pending,
  error,
  onConfirm,
}: {
  login: string;
  isSelf: boolean;
  pending: boolean;
  error: unknown;
  onConfirm: () => void;
}) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button variant="ghost" size="sm" onClick={() => setOpen(true)}>
        {isSelf ? "Leave" : "Remove"}
      </Button>
      <ConfirmDialog
        open={open}
        onOpenChange={setOpen}
        title={isSelf ? "Leave this project?" : `Remove ${login}?`}
        description={
          isSelf
            ? "You lose access to its sites and web dashboards at once. An owner can add you again."
            : `${login} loses access to this project's sites and web dashboards at once.`
        }
        confirmLabel={isSelf ? "Leave project" : "Remove member"}
        pendingLabel={isSelf ? "Leaving" : "Removing"}
        pending={pending}
        error={error}
        onConfirm={onConfirm}
      />
    </>
  );
}

function InviteRow({
  tenantId,
  invite,
  canManage,
}: {
  tenantId: string;
  invite: Invite;
  canManage: boolean;
}) {
  const revoke = useMutation({
    mutationFn: () => api.revokeInvite(tenantId, invite.id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: keys.members(tenantId) }),
  });
  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-2 p-4 sm:px-5">
      <div className="min-w-0 flex-1">
        <p className="truncate font-medium">{invite.github_login}</p>
        <p className="text-sm text-muted-foreground">
          Joins as {invite.role} at first sign-in. Expires {formatDate(invite.expires_at)}.
        </p>
      </div>
      {canManage ? (
        <Button
          variant="ghost"
          size="sm"
          onClick={() => revoke.mutate()}
          disabled={revoke.isPending}
        >
          Revoke
        </Button>
      ) : null}
      {revoke.error ? (
        <div className="basis-full">
          <FormMessage error={revoke.error} />
        </div>
      ) : null}
    </div>
  );
}
