import { useMutation, useQuery } from "@tanstack/react-query";
import { Ban, LogOut, UserMinus, UserPlus } from "lucide-react";
import { type FormEvent, useState } from "react";
import { useNavigate } from "react-router";
import { ApiError } from "@/api/client";
import { api } from "@/api/endpoints";
import { keys, membersQuery } from "@/api/queries";
import type { AddMemberResult, Invite, Member, Members, Role } from "@/api/schemas";
import { Avatar } from "@/components/avatar";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { type Column, DataTable, useRowPages } from "@/components/data-table";
import { FormMessage } from "@/components/error-panel";
import { FilterBar } from "@/components/filter-bar";
import { NativeSelect, SelectField, TextField } from "@/components/form-field";
import { PageHeader } from "@/components/page-header";
import { ErrorState } from "@/components/states";
import { toast } from "@/components/toast";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Tooltip } from "@/components/ui/tooltip";
import { countOf, formatCount, formatDate } from "@/lib/format";
import * as v from "@/lib/validate";
import { useProject } from "@/pages/project-layout";
import { queryClient } from "@/query-client";
import { MembersSkeleton, PAGE_HEADERS } from "./page-frame";

export type AddMemberValues = { login: string; role: Role };
export type AddMemberErrors = Partial<Record<keyof AddMemberValues, string>>;

// the one field takes a github username or a stellar address (contract
// 4.2.5): a G-address names a wallet account, a C-address a passkey account
// (4.1.9), anything else a github login
export function memberIdentity(
  input: string,
): { github_login: string } | { stellar_address: string } {
  const t = input.trim().replace(/^@/, "");
  return /^[GC][A-Z2-7]{55}$/.test(t) ? { stellar_address: t } : { github_login: t };
}

export function validateAddMember(values: AddMemberValues): AddMemberErrors {
  const id = memberIdentity(values.login);
  const code =
    "stellar_address" in id ? v.strkey(id.stellar_address) : v.githubLogin(id.github_login);
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
    if (path === "github_login" || path === "stellar_address") out.login = v.fieldMessage(code);
    else if (path === "role") out.role = v.fieldMessage(code);
    // too_many is reported on the body as a whole
    else out.login ??= v.fieldMessage(code);
  }
  return out;
}

export function addResultText(r: AddMemberResult): string {
  if (r.kind === "member")
    return `${r.member.display_name} is added as ${ROLE_WORD[r.member.role]}.`;
  return `${r.invite.display_name} has no account yet. The invite is open until ${formatDate(r.invite.expires_at)}.`;
}

const ROLE_WORD: Record<Role, string> = { owner: "owner", member: "member" };

// what each role may do, said once on the role field
export const ROLE_HELP =
  "Members manage sites, API tokens and AI keys, and read all data. Owners can also add and remove people, delete sites, accept new terms and request erasure.";

// from this many people the list gets a search field
export const MEMBER_SEARCH_FROM = 8;
const MEMBER_PAGE = 25;

// the members whose name, login or address holds the search text
export function matchMembers(list: Member[], q: string): Member[] {
  const t = q.trim().toLowerCase();
  if (!t) return list;
  return list.filter((m) =>
    [m.display_name, m.github_login, m.stellar_address, m.passkey_address].some((x) =>
      x?.toLowerCase().includes(t),
    ),
  );
}

// a person in a list: the github profile link of a github account, or the
// short address of a wallet or passkey account without one, with the whole
// address on hover
// a member as the table names them: the github login with its @, else the
// display name; a dialog title names them the same way
function shownName(m: Member): string {
  return m.github_login !== null ? `@${m.github_login}` : m.display_name;
}

function PersonName({
  githubLogin,
  address,
  name,
}: {
  githubLogin: string | null;
  address: string | null;
  name: string;
}) {
  if (githubLogin !== null) {
    return (
      <a
        href={`https://github.com/${encodeURIComponent(githubLogin)}`}
        rel="noopener noreferrer"
        className="rounded-sm hover:underline focus-ring"
      >
        @{githubLogin}
      </a>
    );
  }
  return (
    <span className="font-mono" title={address ?? undefined}>
      {name}
    </span>
  );
}

function Initial({ login, dashed }: { login: string; dashed?: boolean }) {
  return <Avatar name={login} dashed={dashed} />;
}

export function MembersPage() {
  const { tenant, me, isOwner, termsCurrent } = useProject();
  const list = useQuery(membersQuery(tenant.id));
  const canManage = isOwner && termsCurrent;
  const [adding, setAdding] = useState(false);

  return (
    <div className="flex flex-col gap-8">
      <PageHeader
        {...PAGE_HEADERS.members}
        primary={
          canManage ? (
            <Button onClick={() => setAdding(true)} className="max-lg:h-(--vs-control-touch)">
              <UserPlus aria-hidden />
              Add a person
            </Button>
          ) : null
        }
      />
      {list.isPending ? (
        <MembersSkeleton />
      ) : list.error ? (
        <ErrorState
          title="The members did not load"
          error={list.error}
          onRetry={() => list.refetch()}
        />
      ) : (
        <MembersBody
          tenantId={tenant.id}
          tenantName={tenant.name}
          selfId={me.user.id}
          canManage={canManage}
          data={list.data}
        />
      )}
      <Dialog
        open={adding}
        onOpenChange={setAdding}
        title="Add a person"
        description="A person without an account gets an invite. They join at their first sign-in."
      >
        {adding ? (
          <div className="pb-4">
            <AddMemberForm tenantId={tenant.id} onDone={() => setAdding(false)} />
          </div>
        ) : null}
      </Dialog>
    </div>
  );
}

type Pending =
  | { kind: "role"; member: Member; role: Role }
  | { kind: "remove"; member: Member; self: boolean };

function MembersBody({
  tenantId,
  tenantName,
  selfId,
  canManage,
  data,
}: {
  tenantId: string;
  tenantName: string;
  selfId: string;
  canManage: boolean;
  data: Members;
}) {
  const navigate = useNavigate();
  const [q, setQ] = useState("");
  const [pending, setPending] = useState<Pending | null>(null);
  const ownerCount = data.members.filter((m) => m.role === "owner").length;
  const lastOwner = (m: Member) => m.role === "owner" && ownerCount === 1;
  const shown = matchMembers(data.members, q);
  const paged = useRowPages(shown, MEMBER_PAGE, q);

  const setRole = useMutation({
    mutationFn: (p: { member: Member; role: Role }) =>
      api.setMemberRole(tenantId, p.member.user_id, p.role),
    onSuccess: async (_, p) => {
      setPending(null);
      toast.success(
        `${p.member.display_name} is now ${p.role === "owner" ? "an owner" : "a member"}.`,
      );
      await queryClient.invalidateQueries({ queryKey: keys.members(tenantId) });
      if (p.member.user_id === selfId) {
        await queryClient.invalidateQueries({ queryKey: keys.tenant(tenantId) });
        await queryClient.invalidateQueries({ queryKey: keys.me });
      }
    },
  });
  const remove = useMutation({
    mutationFn: (m: Member) => api.removeMember(tenantId, m.user_id),
    onSuccess: async (_, m) => {
      setPending(null);
      if (m.user_id === selfId) {
        toast.success(`You left ${tenantName}.`);
        // after leaving, every route of this project answers 404
        queryClient.removeQueries({ queryKey: keys.tenant(tenantId) });
        await queryClient.invalidateQueries({ queryKey: keys.me });
        navigate("/", { replace: true });
        return;
      }
      toast.success(`${m.display_name} is removed.`);
      await queryClient.invalidateQueries({ queryKey: keys.members(tenantId) });
    },
  });

  const columns: Column<Member>[] = [
    {
      key: "member",
      header: "Member",
      primary: true,
      minWidth: "min-w-56",
      cell: (m) => (
        <span className="flex min-w-0 items-center gap-2 py-1">
          <Initial login={m.display_name} />
          <span className="min-w-0 truncate font-medium text-fg">
            <PersonName
              githubLogin={m.github_login}
              address={m.stellar_address ?? m.passkey_address}
              name={m.display_name}
            />
            {m.user_id === selfId ? <span className="font-normal text-fg-2"> (you)</span> : null}
          </span>
        </span>
      ),
    },
    {
      key: "role",
      header: "Role",
      mobile: true,
      minWidth: "min-w-36",
      cell: (m) =>
        canManage ? (
          <RoleSelect
            member={m}
            locked={lastOwner(m)}
            onChange={(role) => {
              setRole.reset();
              setPending({ kind: "role", member: m, role });
            }}
          />
        ) : (
          <span className="text-fg-2">{m.role === "owner" ? "Owner" : "Member"}</span>
        ),
    },
    {
      key: "added",
      header: "Added",
      mobile: true,
      cell: (m) => <span className="whitespace-nowrap text-fg-2">{formatDate(m.created_at)}</span>,
    },
    {
      key: "actions",
      header: "Actions",
      align: "right",
      mobile: true,
      cell: (m) => {
        const self = m.user_id === selfId;
        if (!(canManage || self) || lastOwner(m)) return null;
        return (
          <Button
            variant="destructive-outline"
            size="sm"
            className="max-lg:h-(--vs-control-touch)"
            onClick={() => {
              remove.reset();
              setPending({ kind: "remove", member: m, self });
            }}
          >
            {self ? <LogOut aria-hidden /> : <UserMinus aria-hidden />}
            {self ? "Leave" : "Remove"}
          </Button>
        );
      },
    },
  ];

  const p = pending;
  return (
    <div className="flex flex-col gap-8">
      <section aria-labelledby="members-h" className="flex min-w-0 flex-col gap-4">
        <div className="flex flex-wrap items-end justify-between gap-3">
          <h2 id="members-h" className="text-title text-fg">
            {countOf(data.members.length, "member")}
          </h2>
          {canManage && ownerCount === 1 ? (
            <span className="text-small text-fg-2">
              The last owner cannot leave or become a member.
            </span>
          ) : null}
        </div>
        {data.members.length >= MEMBER_SEARCH_FROM ? (
          <FilterBar
            search={q}
            onSearch={setQ}
            searchLabel="Search members"
            placeholder="Search by name or address"
            onClearAll={() => setQ("")}
          />
        ) : null}
        <DataTable<Member>
          label="Members"
          columns={columns}
          rows={paged.rows}
          getRowKey={(m) => m.user_id}
          rowLabel={(m) => m.display_name}
          rowProps={(m) => ({ "data-member": m.user_id })}
          empty={{ title: "No member matches", text: "Search by another name or address." }}
          footer={paged.footer}
        />
      </section>

      {data.invites.length > 0 ? (
        <InvitesSection tenantId={tenantId} invites={data.invites} canManage={canManage} />
      ) : null}

      <ConfirmDialog
        open={p !== null}
        onOpenChange={(o) => {
          if (!o) setPending(null);
        }}
        tone={p?.kind === "role" ? "primary" : "destructive"}
        title={
          p?.kind === "role"
            ? `Make ${p.member.display_name} ${p.role === "owner" ? "an owner" : "a member"}?`
            : p?.self
              ? `Leave ${tenantName}?`
              : `Remove ${p ? shownName(p.member) : ""}?`
        }
        description={
          p?.kind === "role"
            ? p.role === "owner"
              ? "Owners can add and remove people, delete sites, accept new terms and request erasure."
              : "Members can no longer add or remove people, delete sites, accept terms or request erasure."
            : p?.self
              ? "You lose access to its sites and data at once. An owner can add you again."
              : `${p?.member.display_name ?? ""} loses access to this project's sites and data at once.`
        }
        confirmLabel={
          p?.kind === "role" ? "Change role" : p?.self ? "Leave project" : "Remove member"
        }
        pendingLabel={p?.kind === "role" ? "Changing" : p?.self ? "Leaving" : "Removing"}
        pending={setRole.isPending || remove.isPending}
        error={p?.kind === "role" ? setRole.error : remove.error}
        onConfirm={() => {
          if (!p) return;
          if (p.kind === "role") setRole.mutate({ member: p.member, role: p.role });
          else remove.mutate(p.member);
        }}
      />
    </div>
  );
}

// the role of a member as a select; the change waits for a confirmation.
// the last owner cannot become a member, and the tooltip says why.
function RoleSelect({
  member,
  locked,
  onChange,
}: {
  member: Member;
  locked: boolean;
  onChange: (role: Role) => void;
}) {
  const select = (
    <NativeSelect
      aria-label={`Role of ${member.display_name}`}
      value={member.role}
      disabled={locked}
      className="relative z-(--vs-layer-raised) max-lg:h-(--vs-control-touch)"
      onChange={(e) => onChange(e.target.value as Role)}
    >
      <option value="member">Member</option>
      <option value="owner">Owner</option>
    </NativeSelect>
  );
  if (!locked) return select;
  return (
    <Tooltip content="The last owner cannot become a member.">
      <span className="inline-flex">{select}</span>
    </Tooltip>
  );
}

export function AddMemberForm({ tenantId, onDone }: { tenantId: string; onDone?: () => void }) {
  const [values, setValues] = useState<AddMemberValues>({ login: "", role: "member" });
  const [errors, setErrors] = useState<AddMemberErrors>({});
  const add = useMutation({
    mutationFn: (body: Parameters<typeof api.addMember>[1]) => api.addMember(tenantId, body),
    onSuccess: async (r) => {
      toast.success(addResultText(r));
      setValues({ login: "", role: "member" });
      await queryClient.invalidateQueries({ queryKey: keys.members(tenantId) });
      onDone?.();
    },
    onError: (err) => setErrors(serverMemberErrors(err)),
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    const found = validateAddMember(values);
    setErrors(found);
    if (Object.keys(found).length > 0) return;
    add.mutate({ ...memberIdentity(values.login), role: values.role });
  }

  return (
    <form noValidate onSubmit={submit} aria-label="Add a member" className="flex flex-col gap-4">
      <TextField
        label="GitHub username or Stellar address"
        name="github_login"
        autoCapitalize="none"
        autoComplete="off"
        spellCheck={false}
        maxLength={56}
        placeholder="octocat, G… or C…"
        hint="A G address is a wallet. A C address is a passkey account."
        className="max-lg:h-(--vs-control-touch)"
        value={values.login}
        onChange={(e) => setValues((s) => ({ ...s, login: e.target.value }))}
        error={errors.login}
      />
      <SelectField
        label="Role"
        name="role"
        hint={ROLE_HELP}
        className="max-lg:h-(--vs-control-touch)"
        value={values.role}
        onChange={(e) => setValues((s) => ({ ...s, role: e.target.value as Role }))}
        error={errors.role}
      >
        <option value="member">Member</option>
        <option value="owner">Owner</option>
      </SelectField>
      <FormMessage
        error={
          add.error && Object.keys(serverMemberErrors(add.error)).length === 0 ? add.error : null
        }
      />
      <div className="flex justify-end border-t border-line pt-4">
        <Button type="submit" className="max-lg:h-(--vs-control-touch)" pending={add.isPending}>
          {add.isPending ? "Adding" : "Add"}
        </Button>
      </div>
    </form>
  );
}

export function matchInvites(list: Invite[], q: string): Invite[] {
  const t = q.trim().toLowerCase();
  if (!t) return list;
  return list.filter((i) =>
    [i.display_name, i.github_login, i.stellar_address].some((x) => x?.toLowerCase().includes(t)),
  );
}

// open invites in the members table form: a count in the title, search from
// the same size on, and pages, so a long list stays short
function InvitesSection({
  tenantId,
  invites,
  canManage,
}: {
  tenantId: string;
  invites: Invite[];
  canManage: boolean;
}) {
  const [q, setQ] = useState("");
  const shown = matchInvites(invites, q);
  const paged = useRowPages(shown, MEMBER_PAGE, q);
  const columns: Column<Invite>[] = [
    {
      key: "person",
      header: "Person",
      primary: true,
      minWidth: "min-w-56",
      cell: (i) => (
        <span className="flex min-w-0 items-center gap-2 py-1">
          <Initial login={i.display_name} dashed />
          <span className="min-w-0 truncate font-medium text-fg">
            <PersonName
              githubLogin={i.github_login}
              address={i.stellar_address}
              name={i.display_name}
            />
          </span>
        </span>
      ),
    },
    {
      key: "role",
      header: "Joins as",
      mobile: true,
      cell: (i) => <span className="text-fg-2">{i.role === "owner" ? "Owner" : "Member"}</span>,
    },
    {
      key: "until",
      header: "Open until",
      mobile: true,
      cell: (i) => <span className="whitespace-nowrap text-fg-2">{formatDate(i.expires_at)}</span>,
    },
    ...(canManage
      ? [
          {
            key: "actions",
            header: "Actions",
            align: "right" as const,
            mobile: true,
            cell: (i: Invite) => <RevokeInvite tenantId={tenantId} invite={i} />,
          },
        ]
      : []),
  ];
  return (
    <section aria-labelledby="invites-h" className="flex min-w-0 flex-col gap-4">
      <div className="flex flex-col gap-1">
        <h2 id="invites-h" className="text-title text-fg">
          Open invites ({formatCount(invites.length)})
        </h2>
        <p className="text-small text-fg-2">A person joins with their role at the first sign-in.</p>
      </div>
      {invites.length >= MEMBER_SEARCH_FROM ? (
        <FilterBar
          search={q}
          onSearch={setQ}
          searchLabel="Search invites"
          placeholder="Search by name or address"
          onClearAll={() => setQ("")}
        />
      ) : null}
      <DataTable<Invite>
        label="Open invites"
        columns={columns}
        rows={paged.rows}
        getRowKey={(i) => i.id}
        rowLabel={(i) => i.display_name}
        rowProps={(i) => ({ "data-invite": i.id })}
        empty={{ title: "No invite matches", text: "Search by another name or address." }}
        footer={paged.footer}
      />
    </section>
  );
}

function RevokeInvite({ tenantId, invite }: { tenantId: string; invite: Invite }) {
  const [open, setOpen] = useState(false);
  const revoke = useMutation({
    mutationFn: () => api.revokeInvite(tenantId, invite.id),
    onSuccess: async () => {
      setOpen(false);
      toast.success(`The invite for ${invite.display_name} is revoked.`);
      await queryClient.invalidateQueries({ queryKey: keys.members(tenantId) });
    },
  });
  return (
    <>
      <Button
        variant="destructive-outline"
        size="sm"
        className="max-lg:h-(--vs-control-touch)"
        onClick={() => {
          revoke.reset();
          setOpen(true);
        }}
      >
        <Ban aria-hidden />
        Revoke
      </Button>
      <ConfirmDialog
        open={open}
        onOpenChange={setOpen}
        title={`Revoke the invite for ${invite.display_name}?`}
        description="They cannot join with this invite. You can invite them again."
        confirmLabel="Revoke invite"
        pendingLabel="Revoking"
        pending={revoke.isPending}
        error={revoke.error}
        onConfirm={() => revoke.mutate()}
      />
    </>
  );
}
