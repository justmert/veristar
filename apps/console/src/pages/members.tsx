import { type UseQueryResult, useMutation, useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { UserPlus } from "lucide-react";
import { type FormEvent, useState } from "react";
import { useNavigate } from "react-router";
import { ApiError } from "@/api/client";
import { api } from "@/api/endpoints";
import { keys, membersQuery } from "@/api/queries";
import type { AddMemberResult, Invite, Member, Members, Role } from "@/api/schemas";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { ErrorPanel, FormMessage, Loading } from "@/components/error-panel";
import { NativeSelect, SelectField, TextField } from "@/components/form-field";
import { Stagger, StaggerItem } from "@/components/motion";
import { projectTabs } from "@/components/nav/nav-items";
import { NavTabs } from "@/components/nav/nav-tabs";
import { PageHeader, Panel } from "@/components/panel";
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

  return (
    <div className="flex max-w-[61rem] flex-col gap-6">
      <PageHeader title="Settings" />
      <NavTabs label="Settings" items={projectTabs(tenant.id, "settings") ?? []} />
      <MembersBody
        tenantId={tenant.id}
        tenantName={tenant.name}
        selfId={me.user.id}
        canManage={isOwner && termsCurrent}
        list={list}
      />
    </div>
  );
}

function MembersBody({
  tenantId,
  tenantName,
  selfId,
  canManage,
  list,
}: {
  tenantId: string;
  tenantName: string;
  selfId: string;
  canManage: boolean;
  list: UseQueryResult<Members>;
}) {
  if (list.isPending) return <Loading label="Loading members" />;
  if (list.error) return <ErrorPanel error={list.error} onRetry={() => list.refetch()} />;
  const ownerCount = list.data.members.filter((m) => m.role === "owner").length;

  return (
    <div className="flex animate-rise-in flex-col gap-6">
      <section aria-labelledby="members-h" className="flex flex-col gap-4">
        <h2 id="members-h" className="sr-only">
          Members
        </h2>
        {canManage ? <AddMemberForm tenantId={tenantId} /> : null}
        <div className="grid gap-3 text-small text-fg-2 sm:grid-cols-2">
          <p className="rounded-lg border border-line px-3.5 py-3">
            <span className="font-medium text-fg">Owners</span> also add and remove people, delete
            sites, accept new terms and request erasure.
          </p>
          <p className="rounded-lg border border-line px-3.5 py-3">
            <span className="font-medium text-fg">Members</span> manage sites and open the web
            dashboards.
          </p>
        </div>
        <Panel flush>
          <div className="relative overflow-x-auto">
            <table className="w-full min-w-[36rem] border-separate border-spacing-0 text-body">
              <thead>
                <tr className="text-left text-caption text-fg-3">
                  <th scope="col" className="border-b border-line px-4 py-2.5 font-medium">
                    Member
                  </th>
                  <th scope="col" className="border-b border-line px-4 py-2.5 font-medium">
                    Role
                  </th>
                  <th scope="col" className="border-b border-line px-4 py-2.5 font-medium">
                    Added
                  </th>
                  <th scope="col" className="border-b border-line px-4 py-2.5">
                    <span className="sr-only">Actions</span>
                  </th>
                </tr>
              </thead>
              <Stagger as="tbody" className="[&_tr:last-child>td]:border-0">
                {list.data.members.map((m) => (
                  <MemberRow
                    key={m.user_id}
                    tenantId={tenantId}
                    tenantName={tenantName}
                    member={m}
                    isSelf={m.user_id === selfId}
                    canManage={canManage}
                    lastOwner={m.role === "owner" && ownerCount === 1}
                  />
                ))}
              </Stagger>
            </table>
          </div>
        </Panel>
      </section>

      {list.data.invites.length > 0 ? (
        <section aria-labelledby="invites-h" className="flex flex-col gap-3">
          <h2 id="invites-h" className="text-title text-fg">
            Open invites
          </h2>
          <Panel flush>
            <Stagger as="ul" className="divide-y divide-line-subtle">
              {list.data.invites.map((i) => (
                <StaggerItem as="li" key={i.id}>
                  <InviteRow tenantId={tenantId} invite={i} canManage={canManage} />
                </StaggerItem>
              ))}
            </Stagger>
          </Panel>
        </section>
      ) : null}
    </div>
  );
}

function Initial({ login, dashed }: { login: string; dashed?: boolean }) {
  return (
    <span
      aria-hidden
      className={cn(
        "flex size-7 shrink-0 items-center justify-center rounded-full text-caption font-semibold",
        dashed ? "border border-dashed border-line-strong text-fg-2" : "bg-muted text-fg",
      )}
    >
      {login.slice(0, 1).toUpperCase()}
    </span>
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
    <form noValidate onSubmit={submit} aria-label="Add a member" className="flex flex-col gap-3">
      <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_10rem_auto] sm:items-start">
        <TextField
          label="GitHub username"
          name="github_login"
          autoCapitalize="none"
          autoComplete="off"
          spellCheck={false}
          maxLength={40}
          placeholder="octocat"
          hint="Someone without an account yet gets an invite and joins at first sign-in."
          className="max-lg:h-(--vs-control-touch)"
          value={values.login}
          onChange={(e) => setValues((s) => ({ ...s, login: e.target.value }))}
          error={errors.login}
        />
        <SelectField
          label="Role"
          name="role"
          className="max-lg:h-(--vs-control-touch)"
          value={values.role}
          onChange={(e) => setValues((s) => ({ ...s, role: e.target.value as Role }))}
          error={errors.role}
        >
          <option value="member">Member</option>
          <option value="owner">Owner</option>
        </SelectField>
        <Button
          type="submit"
          className="sm:mt-6.5 max-lg:h-(--vs-control-touch)"
          pending={add.isPending}
        >
          {add.isPending ? null : <UserPlus aria-hidden />}
          {add.isPending ? "Adding" : "Add"}
        </Button>
      </div>
      {done ? (
        <p role="status" className="animate-fade-in text-small text-success">
          {done}
        </p>
      ) : null}
      <FormMessage
        error={
          add.error && Object.keys(serverMemberErrors(add.error)).length === 0 ? add.error : null
        }
      />
    </form>
  );
}

const cell = "border-b border-line-subtle px-4 py-3 align-middle";

function MemberRow({
  tenantId,
  tenantName,
  member,
  isSelf,
  canManage,
  lastOwner,
}: {
  tenantId: string;
  tenantName: string;
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
    <StaggerItem
      as="tr"
      className="transition-colors duration-(--vs-duration-press) ease-standard hover:bg-hover"
    >
      <td className={cell}>
        <span className="flex min-w-0 items-center gap-2.5">
          <Initial login={member.github_login} />
          <span className="min-w-0 truncate font-medium text-fg">
            <a
              href={`https://github.com/${encodeURIComponent(member.github_login)}`}
              rel="noopener noreferrer"
              className="rounded-sm hover:underline focus-ring"
            >
              @{member.github_login}
            </a>
            {isSelf ? <span className="font-normal text-fg-3"> (you)</span> : null}
          </span>
        </span>
      </td>
      <td className={cell}>
        {canManage ? (
          <NativeSelect
            aria-label={`Role of ${member.github_login}`}
            value={member.role}
            disabled={setRole.isPending || lastOwner}
            title={lastOwner ? "The last owner cannot be made a member" : undefined}
            className="max-lg:h-(--vs-control-touch)"
            onChange={(e) => setRole.mutate(e.target.value as Role)}
          >
            <option value="member">Member</option>
            <option value="owner">Owner</option>
          </NativeSelect>
        ) : (
          <span className="text-small text-fg-2">
            {member.role === "owner" ? "Owner" : "Member"}
          </span>
        )}
        {canManage && lastOwner ? (
          <span className="mt-1 block text-caption font-normal text-fg-3">
            The last owner cannot be made a member or removed.
          </span>
        ) : null}
        {setRole.error ? <FormMessage error={setRole.error} className="mt-2" /> : null}
      </td>
      <td className={cn(cell, "whitespace-nowrap text-small text-fg-2")}>
        {formatDate(member.created_at)}
      </td>
      <td className={cn(cell, "text-right")}>
        {(canManage || isSelf) && !lastOwner ? (
          <RemoveMember
            login={member.github_login}
            tenantName={tenantName}
            isSelf={isSelf}
            pending={remove.isPending}
            error={remove.error}
            onConfirm={() => remove.mutate()}
            onClose={() => remove.reset()}
          />
        ) : null}
      </td>
    </StaggerItem>
  );
}

function RemoveMember({
  login,
  tenantName,
  isSelf,
  pending,
  error,
  onConfirm,
  onClose,
}: {
  login: string;
  tenantName: string;
  isSelf: boolean;
  pending: boolean;
  error: unknown;
  onConfirm: () => void;
  onClose: () => void;
}) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button
        variant="ghost"
        size="sm"
        className={cn(
          "max-lg:h-(--vs-control-touch)",
          isSelf ? "text-fg-2" : "text-fg-2 hover:text-critical",
        )}
        onClick={() => setOpen(true)}
      >
        {isSelf ? "Leave" : "Remove"}
      </Button>
      <ConfirmDialog
        open={open}
        onOpenChange={(o) => {
          setOpen(o);
          if (!o) onClose();
        }}
        title={isSelf ? `Leave ${tenantName}?` : `Remove ${login}?`}
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
    <div className="flex flex-wrap items-center gap-x-3 gap-y-2 px-4 py-3 transition-colors duration-(--vs-duration-press) ease-standard hover:bg-hover">
      <Initial login={invite.github_login} dashed />
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <p className="truncate text-body font-medium text-fg">
          @{invite.github_login}
          <span className="ml-2 text-caption font-normal text-warning">Has not signed in yet</span>
        </p>
        <p className="text-small text-fg-2">
          Joins as {invite.role} at first sign-in. Expires {formatDate(invite.expires_at)}.
        </p>
      </div>
      {canManage ? (
        <Button
          variant="ghost"
          size="sm"
          className="text-fg-2 max-lg:h-(--vs-control-touch)"
          onClick={() => revoke.mutate()}
          pending={revoke.isPending}
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
