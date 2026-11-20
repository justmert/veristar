import { useMutation, useQuery } from "@tanstack/react-query";
import { Ban, ChevronDown, LogOut, Plus, UserCog, UserMinus } from "lucide-react";
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
import { TextField } from "@/components/form-field";
import { PageHeader } from "@/components/page-header";
import type { RowAction } from "@/components/row-menu";
import { Segmented } from "@/components/segmented";
import { ErrorState } from "@/components/states";
import { Pill } from "@/components/status";
import { toast } from "@/components/toast";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Tooltip } from "@/components/ui/tooltip";
import { countOf, formatDate } from "@/lib/format";
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

export function MembersPage() {
  const { tenant, me, isOwner, termsCurrent } = useProject();
  const list = useQuery(membersQuery(tenant.id));
  const canManage = isOwner && termsCurrent;
  const [adding, setAdding] = useState(false);
  const data = list.data;
  const owners = data?.members.filter((m) => m.role === "owner").length ?? 0;
  const members = (data?.members.length ?? 0) - owners;
  const invites = data?.invites.length ?? 0;

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        {...PAGE_HEADERS.members}
        items={
          data
            ? [
                <span key="o">{countOf(owners, "owner")}</span>,
                members > 0 ? <span key="m">{countOf(members, "member")}</span> : null,
                invites > 0 ? <span key="i">{countOf(invites, "invite")}</span> : null,
              ]
            : undefined
        }
        primary={
          canManage ? (
            <Button onClick={() => setAdding(true)} className="max-lg:h-(--vs-control-touch)">
              <Plus aria-hidden />
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
        <People
          tenantId={tenant.id}
          tenantName={tenant.name}
          selfId={me.user.id}
          canManage={canManage}
          data={list.data}
        />
      )}
      <Dialog open={adding} onOpenChange={setAdding} title="Add a person">
        {adding ? (
          <AddMemberForm
            tenantId={tenant.id}
            onDone={() => setAdding(false)}
            onCancel={() => setAdding(false)}
          />
        ) : null}
      </Dialog>
    </div>
  );
}

// one row per person: a member of the project, or an invite waiting for an
// account
type Person = { kind: "member"; member: Member } | { kind: "invite"; invite: Invite };

const personKey = (p: Person) => (p.kind === "member" ? p.member.user_id : `invite-${p.invite.id}`);

type Pending = { member: Member; role: Role };

function People({
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
  const people: Person[] = [
    ...matchMembers(data.members, q).map((member) => ({ kind: "member" as const, member })),
    ...matchInvites(data.invites, q).map((invite) => ({ kind: "invite" as const, invite })),
  ];
  const total = data.members.length + data.invites.length;
  const paged = useRowPages(people, MEMBER_PAGE, q);

  const setRole = useMutation({
    mutationFn: (p: Pending) => api.setMemberRole(tenantId, p.member.user_id, p.role),
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
  // the row menu's confirmation runs this; leaving takes the person home
  async function remove(m: Member) {
    await api.removeMember(tenantId, m.user_id);
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
  }
  const askRole = (m: Member, role: Role) => {
    setRole.reset();
    setPending({ member: m, role });
  };

  const rowActions = (p: Person): RowAction[] => {
    if (p.kind === "invite") {
      const i = p.invite;
      return canManage
        ? [
            {
              label: "Revoke invite",
              icon: <Ban aria-hidden />,
              destructive: true,
              confirm: {
                title: `Revoke the invite for ${i.display_name}?`,
                description: "They cannot join with this invite. You can invite them again.",
                confirmLabel: "Revoke invite",
                pendingLabel: "Revoking",
                run: async () => {
                  await api.revokeInvite(tenantId, i.id);
                  toast.success(`The invite for ${i.display_name} is revoked.`);
                  await queryClient.invalidateQueries({ queryKey: keys.members(tenantId) });
                },
              },
            },
          ]
        : [];
    }
    const m = p.member;
    const self = m.user_id === selfId;
    const out: RowAction[] = [];
    if (canManage && !lastOwner(m))
      out.push({
        label: m.role === "owner" ? "Make member" : "Make owner",
        icon: <UserCog aria-hidden />,
        onSelect: () => askRole(m, m.role === "owner" ? "member" : "owner"),
      });
    if ((canManage || self) && !lastOwner(m))
      out.push({
        label: self ? "Leave project" : "Remove",
        icon: self ? <LogOut aria-hidden /> : <UserMinus aria-hidden />,
        destructive: true,
        confirm: {
          title: self ? `Leave ${tenantName}?` : `Remove ${shownName(m)}?`,
          description: self
            ? "You lose access to its sites and data at once. An owner can add you again."
            : `${m.display_name} loses access to this project's sites and data at once.`,
          confirmLabel: self ? "Leave project" : "Remove member",
          pendingLabel: self ? "Leaving" : "Removing",
          run: () => remove(m),
        },
      });
    return out;
  };

  const columns: Column<Person>[] = [
    {
      key: "person",
      header: "Person",
      primary: true,
      minWidth: "min-w-56",
      className: "w-[48%]",
      cell: (p) => {
        const name = p.kind === "member" ? p.member.display_name : p.invite.display_name;
        const login = p.kind === "member" ? p.member.github_login : p.invite.github_login;
        const address =
          p.kind === "member"
            ? (p.member.stellar_address ?? p.member.passkey_address)
            : p.invite.stellar_address;
        return (
          <span className="flex min-w-0 items-center gap-3 py-1">
            <Avatar name={name} dashed={p.kind === "invite"} />
            <span className="min-w-0 truncate font-medium text-fg">
              <PersonName githubLogin={login} address={address} name={name} />
            </span>
            {p.kind === "member" && p.member.user_id === selfId ? (
              <span className="shrink-0 rounded-md border border-line-strong px-1.5 text-caption text-fg-2">
                You
              </span>
            ) : null}
          </span>
        );
      },
    },
    {
      key: "role",
      header: "Role",
      mobile: true,
      minWidth: "min-w-32",
      cell: (p) =>
        p.kind === "member" && canManage ? (
          <RoleSelect
            member={p.member}
            locked={lastOwner(p.member)}
            onChange={(role) => askRole(p.member, role)}
          />
        ) : (
          <span className="text-fg">
            {(p.kind === "member" ? p.member.role : p.invite.role) === "owner" ? "Owner" : "Member"}
          </span>
        ),
    },
    {
      key: "status",
      header: "Status",
      mobile: true,
      minWidth: "min-w-44",
      cell: (p) =>
        p.kind === "member" ? (
          <span className="whitespace-nowrap text-fg-2">
            Since {formatDate(p.member.created_at)}
          </span>
        ) : (
          <span className="flex flex-wrap items-center gap-2.5">
            <Pill tone="info">Invited</Pill>
            <span className="whitespace-nowrap text-small text-fg-2">
              until {formatDate(p.invite.expires_at)}
            </span>
          </span>
        ),
    },
  ];

  const p = pending;
  return (
    <div className="flex flex-col gap-4">
      {total >= MEMBER_SEARCH_FROM ? (
        <FilterBar
          search={q}
          onSearch={setQ}
          searchLabel="Search people"
          placeholder="Search by name or address"
          onClearAll={() => setQ("")}
        />
      ) : null}
      <DataTable<Person>
        label="Members"
        columns={columns}
        rows={paged.rows}
        getRowKey={personKey}
        rowLabel={(x) => (x.kind === "member" ? x.member.display_name : x.invite.display_name)}
        rowProps={(x): Record<`data-${string}`, string> =>
          x.kind === "member" ? { "data-member": x.member.user_id } : { "data-invite": x.invite.id }
        }
        empty={{ title: "No person matches" }}
        footer={paged.footer}
        rowActions={rowActions}
      />

      <ConfirmDialog
        open={p !== null}
        onOpenChange={(o) => {
          if (!o) setPending(null);
        }}
        tone="primary"
        title={
          p ? `Make ${p.member.display_name} ${p.role === "owner" ? "an owner" : "a member"}?` : ""
        }
        description={
          p?.role === "owner"
            ? "Owners can add and remove people, delete sites, accept new terms and request erasure."
            : "Members can no longer add or remove people, delete sites, accept terms or request erasure."
        }
        confirmLabel="Change role"
        pendingLabel="Changing"
        pending={setRole.isPending}
        error={setRole.error}
        onConfirm={() => {
          if (p) setRole.mutate(p);
        }}
      />
    </div>
  );
}

// the role of a member as a quiet select in the row; the change waits for a
// confirmation. the last owner cannot become a member, and the tooltip says
// why.
function RoleSelect({
  member,
  locked,
  onChange,
}: {
  member: Member;
  locked: boolean;
  onChange: (role: Role) => void;
}) {
  // pulled left by its inner padding, so the role text lines up with the
  // column heading and with the plain role of an invite
  const select = (
    <span className="relative -ml-1.5 inline-flex items-center">
      <select
        aria-label={`Role of ${member.display_name}`}
        value={member.role}
        disabled={locked}
        onChange={(e) => onChange(e.target.value as Role)}
        className="relative z-(--vs-layer-raised) h-8 cursor-pointer appearance-none rounded-md bg-transparent pr-6 pl-1.5 text-body text-fg transition-colors duration-(--vs-duration-press) hover:bg-hover focus-ring disabled:cursor-not-allowed disabled:text-fg-2 max-lg:h-(--vs-control-touch)"
      >
        <option value="member">Member</option>
        <option value="owner">Owner</option>
      </select>
      <ChevronDown
        aria-hidden
        className="pointer-events-none absolute right-1.5 z-(--vs-layer-raised) size-3.5 text-fg-3"
      />
    </span>
  );
  if (!locked) return select;
  return (
    <Tooltip content="The last owner cannot become a member.">
      <span className="inline-flex">{select}</span>
    </Tooltip>
  );
}

export function AddMemberForm({
  tenantId,
  onDone,
  onCancel,
}: {
  tenantId: string;
  onDone?: () => void;
  onCancel?: () => void;
}) {
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
    <form noValidate onSubmit={submit} aria-label="Add a member" className="flex flex-col gap-5">
      <TextField
        label="GitHub username or Stellar address"
        name="github_login"
        autoCapitalize="none"
        autoComplete="off"
        spellCheck={false}
        maxLength={56}
        placeholder="octocat, G… or C…"
        className="max-lg:h-(--vs-control-touch)"
        value={values.login}
        onChange={(e) => setValues((s) => ({ ...s, login: e.target.value }))}
        error={errors.login}
      />
      <div className="flex flex-col gap-2">
        <span className="text-small font-medium text-fg">Role</span>
        <Tooltip content={ROLE_HELP}>
          <span className="w-fit">
            <Segmented<Role>
              label="Role"
              value={values.role}
              options={[
                { value: "member", label: "Member" },
                { value: "owner", label: "Owner" },
              ]}
              onChange={(role) => setValues((s) => ({ ...s, role }))}
            />
          </span>
        </Tooltip>
        {errors.role ? <p className="text-small text-critical">{errors.role}</p> : null}
      </div>
      <FormMessage
        error={
          add.error && Object.keys(serverMemberErrors(add.error)).length === 0 ? add.error : null
        }
      />
      <div className="-mx-5 flex justify-end gap-2 border-t border-line px-5 pt-3.5 pb-2.5">
        {onCancel ? (
          <Button
            type="button"
            variant="ghost"
            className="max-lg:h-(--vs-control-touch)"
            onClick={onCancel}
          >
            Cancel
          </Button>
        ) : null}
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
