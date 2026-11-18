import { useMutation, useQuery } from "@tanstack/react-query";
import { CirclePause, KeyRound, MoreHorizontal, UserMinus } from "lucide-react";
import { type FormEvent, useState } from "react";
import { ApiError } from "@/api/client";
import {
  operatorKeys,
  operatorsApi,
  type RosterAction,
  type RosterEntry,
  type RosterRole,
  rosterQuery,
} from "@/api/operators";
import { keys, meQuery } from "@/api/queries";
import type { User } from "@/api/schemas";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { type Column, DataTable } from "@/components/data-table";
import { FormMessage } from "@/components/error-panel";
import { SelectField, TextField } from "@/components/form-field";
import { PageHeader } from "@/components/page-header";
import { ErrorState, LoadingState, Skeleton } from "@/components/states";
import { Pill } from "@/components/status";
import { toast } from "@/components/toast";
import { Button } from "@/components/ui/button";
import { Menu, MenuContent, MenuItem, MenuSeparator, MenuTrigger } from "@/components/ui/menu";
import { countOf, formatDateTimeUtc } from "@/lib/format";
import { shortAddress } from "@/lib/stellar-wallet";
import { fieldMessage, githubLogin, strkey } from "@/lib/validate";
import { queryClient } from "@/query-client";

const ROLE_TEXT: Record<RosterRole, string> = {
  operator: "Operator",
  operator_admin: "Operator admin",
};

const REASON_HINT = "Recorded in the audit log with your name.";

function refresh() {
  void queryClient.invalidateQueries({ queryKey: operatorKeys.roster });
  void queryClient.invalidateQueries({ queryKey: keys.me });
}

// the roster rows that name the signed-in account
function mine(e: RosterEntry, u: User) {
  return (
    (e.github_id !== null && e.github_id === u.github_id) ||
    (e.stellar_address !== null && e.stellar_address === u.stellar_address)
  );
}

// the operator roster (contract 5.16). every operator sees it; operator
// admins change it, and every change asks for a reason for the audit log.
// a person's own passkeys are on the Account page.
export function OperatorsPage() {
  const me = useQuery(meQuery);
  const roster = useQuery(rosterQuery);
  const admin =
    me.data && roster.data
      ? roster.data.items.some(
          (e) => mine(e, me.data.user) && e.status === "active" && e.role === "operator_admin",
        )
      : false;
  const error = me.error ?? roster.error;
  return (
    <div className="flex min-w-0 flex-col gap-8">
      <PageHeader
        title="Operators"
        description="The people with access to the operator area. Operator admins add, suspend and remove operators."
      />
      {me.isPending || roster.isPending ? (
        <LoadingState label="Loading the operators">
          <Skeleton.Table rows={4} columns={4} />
        </LoadingState>
      ) : error ? (
        <ErrorState
          title="The operators did not load"
          error={error}
          onRetry={() => {
            me.refetch();
            roster.refetch();
          }}
        />
      ) : me.data && roster.data ? (
        <>
          <RosterSection items={roster.data.items} admin={admin} me={me.data.user} />
          {admin ? <AddOperator /> : null}
        </>
      ) : null}
    </div>
  );
}

// a deploy-settings row knows only the github id; once that person has an
// account, its display name is the github login
function identity(e: RosterEntry) {
  if (e.kind === "github")
    return e.github_login ?? (e.account?.display_name || `GitHub id ${e.github_id}`);
  return e.stellar_address ? shortAddress(e.stellar_address) : "";
}

type Pending = { entry: RosterEntry; action: RosterAction | RosterRole };

const ACTION_TEXT: Record<
  RosterAction | RosterRole,
  { title: string; confirm: string; pending: string; tone: "destructive" | "primary" }
> = {
  suspend: { title: "Suspend", confirm: "Suspend", pending: "Suspending", tone: "destructive" },
  resume: { title: "Resume", confirm: "Resume", pending: "Resuming", tone: "primary" },
  remove: { title: "Remove", confirm: "Remove", pending: "Removing", tone: "destructive" },
  "reset-passkeys": {
    title: "Reset the passkeys of",
    confirm: "Reset passkeys",
    pending: "Resetting",
    tone: "destructive",
  },
  operator_admin: {
    title: "Give operator admin rights to",
    confirm: "Give admin rights",
    pending: "Saving",
    tone: "primary",
  },
  operator: {
    title: "Take operator admin rights from",
    confirm: "Take admin rights",
    pending: "Saving",
    tone: "primary",
  },
};

const ACTION_DETAIL: Record<RosterAction | RosterRole, string> = {
  suspend:
    "The person loses the operator area at once. Their console sessions end. Their AI assistant connections with operator access are revoked.",
  resume: "The person gets the operator area back after the next sign-in and passkey check.",
  remove: "The person leaves the roster. Their passkeys are deleted and their sessions end.",
  "reset-passkeys":
    "Every passkey of the person is deleted and their sessions end. A new window to register a passkey opens now.",
  operator_admin: "Operator admins add, suspend and remove operators.",
  operator: "The person keeps the operator area but cannot change the roster.",
};

const ACTION_DONE: Record<RosterAction | RosterRole, string> = {
  suspend: "is suspended",
  resume: "is active again",
  remove: "is removed from the roster",
  "reset-passkeys": "has no passkeys now. A new window to register one is open",
  operator_admin: "is an operator admin now",
  operator: "is an operator now",
};

function kindText(e: RosterEntry): string {
  return e.kind === "github" ? "GitHub" : e.kind === "passkey" ? "Passkey" : "Stellar wallet";
}

function RosterSection({ items, admin, me }: { items: RosterEntry[]; admin: boolean; me: User }) {
  const [pending, setPending] = useState<Pending | null>(null);
  const m = useMutation({
    mutationFn: async ({ p, reason }: { p: Pending; reason: string }): Promise<void> => {
      if (p.action === "operator" || p.action === "operator_admin")
        await operatorsApi.setRole(p.entry.id, p.action, reason);
      else await operatorsApi.rosterAction(p.entry.id, p.action, reason);
    },
    onSuccess: (_d, { p }) => {
      setPending(null);
      toast.success(`${identity(p.entry)} ${ACTION_DONE[p.action]}.`);
      refresh();
    },
  });
  const t = pending ? ACTION_TEXT[pending.action] : null;
  const columns: Column<RosterEntry>[] = [
    {
      key: "operator",
      header: "Operator",
      primary: true,
      minWidth: "min-w-56",
      cell: (e) => (
        <span className="flex min-w-0 flex-wrap items-center gap-2">
          <span className="min-w-0 truncate text-fg">{identity(e)}</span>
          <span className="text-caption text-fg-2">{kindText(e)}</span>
          {e.bootstrap ? <Pill tone="info">Deploy settings</Pill> : null}
          {mine(e, me) ? <Pill tone="info">You</Pill> : null}
        </span>
      ),
    },
    { key: "role", header: "Role", mobile: true, cell: (e) => ROLE_TEXT[e.role] },
    {
      key: "status",
      header: "Status",
      mobile: true,
      cell: (e) => (
        <span className="flex min-w-0 flex-col items-start gap-1">
          {e.status === "active" ? (
            <Pill tone="success">Active</Pill>
          ) : (
            <Pill tone="warning">Suspended</Pill>
          )}
          {e.status_reason ? (
            <span className="truncate text-caption text-fg-2">{e.status_reason}</span>
          ) : null}
        </span>
      ),
    },
    {
      key: "account",
      header: "Sign-in",
      cell: (e) => (
        <span className="flex flex-col text-small text-fg-2">
          {e.account ? (
            <>
              <span>{countOf(e.account.passkeys, "passkey")}</span>
              <span className="text-caption">
                {e.account.last_login_at
                  ? `Last sign-in ${formatDateTimeUtc(e.account.last_login_at)}`
                  : "Never signed in"}
              </span>
            </>
          ) : (
            <span>No sign-in yet</span>
          )}
          {e.enrollment_until ? (
            <span className="text-caption">
              First passkey before {formatDateTimeUtc(e.enrollment_until)}
            </span>
          ) : null}
        </span>
      ),
    },
  ];
  if (admin)
    columns.push({
      key: "actions",
      mobile: true,
      header: "Actions",
      align: "right",
      cell: (e) => <RowActions entry={e} onPick={(action) => setPending({ entry: e, action })} />,
    });
  return (
    <section aria-labelledby="roster-h" className="flex min-w-0 flex-col gap-3">
      <h2 id="roster-h" className="text-title text-fg">
        Roster
      </h2>
      <DataTable
        label="Roster"
        columns={columns}
        rows={items}
        getRowKey={(e) => e.id}
        rowLabel={(e) => identity(e)}
        rowProps={(e) => ({ "data-roster": e.id })}
      />
      <ConfirmDialog
        open={pending !== null}
        onOpenChange={(o) => {
          if (!o) {
            setPending(null);
            m.reset();
          }
        }}
        title={t && pending ? `${t.title} ${identity(pending.entry)}?` : ""}
        description={pending ? ACTION_DETAIL[pending.action] : ""}
        reason={{ required: true, hint: REASON_HINT, maxLength: 500 }}
        confirmLabel={t?.confirm ?? ""}
        pendingLabel={t?.pending ?? ""}
        tone={t?.tone ?? "primary"}
        pending={m.isPending}
        error={m.error}
        onConfirm={(reason) => pending && m.mutate({ p: pending, reason })}
      />
    </section>
  );
}

// one menu per row: the everyday changes first, the destructive ones last
function RowActions({
  entry,
  onPick,
}: {
  entry: RosterEntry;
  onPick: (a: RosterAction | RosterRole) => void;
}) {
  return (
    <Menu>
      <MenuTrigger asChild>
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label={`Actions for ${identity(entry)}`}
          className="max-lg:size-(--vs-control-touch)"
        >
          <MoreHorizontal aria-hidden />
        </Button>
      </MenuTrigger>
      <MenuContent align="end">
        {entry.bootstrap ? null : (
          <>
            <MenuItem
              onSelect={() =>
                onPick(entry.role === "operator_admin" ? "operator" : "operator_admin")
              }
            >
              {entry.role === "operator_admin" ? "Make operator" : "Make operator admin"}
            </MenuItem>
            {entry.status === "active" ? null : (
              <MenuItem onSelect={() => onPick("resume")}>Resume</MenuItem>
            )}
          </>
        )}
        <MenuItem tone="critical" onSelect={() => onPick("reset-passkeys")}>
          <KeyRound aria-hidden />
          Reset passkeys
        </MenuItem>
        {entry.bootstrap ? null : (
          <>
            {entry.status === "active" ? (
              <MenuItem tone="critical" onSelect={() => onPick("suspend")}>
                <CirclePause aria-hidden />
                Suspend
              </MenuItem>
            ) : null}
            <MenuSeparator />
            <MenuItem tone="critical" onSelect={() => onPick("remove")}>
              <UserMinus aria-hidden />
              Remove from the roster
            </MenuItem>
          </>
        )}
      </MenuContent>
    </Menu>
  );
}

// the roster's add rules (5.16): the identity of its kind, a reason of 1 to
// 500 characters after trimming. the server checks them again.
export function validateAddOperator(form: {
  kind: "github" | "stellar";
  value: string;
  reason: string;
}): { identity?: string; reason?: string } {
  const out: { identity?: string; reason?: string } = {};
  const v = form.value.trim();
  const id = form.kind === "github" ? githubLogin(v) : v === "" ? "required" : strkey(v);
  if (id) out.identity = fieldMessage(id);
  const r = [...form.reason.trim()].length;
  if (r === 0) out.reason = "Enter a reason. It is recorded in the audit log.";
  else if (r > 500) out.reason = "Use at most 500 characters.";
  return out;
}

export function AddOperator() {
  const [kind, setKind] = useState<"github" | "stellar">("github");
  const [value, setValue] = useState("");
  const [role, setRole] = useState<RosterRole>("operator");
  const [reason, setReason] = useState("");
  const [errors, setErrors] = useState<{ identity?: string; reason?: string }>({});
  const m = useMutation({
    mutationFn: () =>
      operatorsApi.addOperator({
        ...(kind === "github" ? { github_login: value.trim() } : { stellar_address: value.trim() }),
        role,
        reason: reason.trim(),
      }),
    onSuccess: () => {
      toast.success(`${value.trim()} is added. They can register a passkey now.`);
      setValue("");
      setReason("");
      refresh();
    },
  });
  // a 422 names its fields (2.5); each is shown at its field
  const fields = m.error instanceof ApiError ? m.error.fields : {};
  const serverField = (k: string) => (fields[k] ? fieldMessage(fields[k]) : undefined);
  const identityError =
    errors.identity ?? serverField(kind === "github" ? "github_login" : "stellar_address");
  const reasonError = errors.reason ?? serverField("reason");
  function submit(e: FormEvent) {
    e.preventDefault();
    const v = validateAddOperator({ kind, value, reason });
    setErrors(v);
    if (v.identity || v.reason) return;
    m.mutate();
  }
  return (
    <section aria-labelledby="add-operator-h" className="flex max-w-prose flex-col gap-4">
      <div className="flex flex-col gap-1">
        <h2 id="add-operator-h" className="text-title text-fg">
          Add an operator
        </h2>
        <p className="text-small text-fg-2">
          The person signs in with this identity. Then they register a passkey within 24 hours,
          unless the deployment sets another time. Reset the passkeys to open a new window.
        </p>
      </div>
      <form onSubmit={submit} className="grid gap-4" noValidate aria-label="Add an operator">
        <SelectField
          label="Signs in with"
          name="kind"
          value={kind}
          onChange={(e) => setKind(e.target.value as "github" | "stellar")}
        >
          <option value="github">GitHub</option>
          <option value="stellar">Stellar wallet or passkey</option>
        </SelectField>
        <TextField
          label={kind === "github" ? "GitHub login" : "Stellar address"}
          hint={
            kind === "github"
              ? undefined
              : "A wallet's G-address, or the C-address of a passkey account from the person's Account page."
          }
          name="identity"
          value={value}
          onChange={(e) => setValue(e.target.value)}
          autoComplete="off"
          spellCheck={false}
          error={identityError}
        />
        <SelectField
          label="Role"
          name="role"
          value={role}
          onChange={(e) => setRole(e.target.value as RosterRole)}
        >
          <option value="operator">Operator</option>
          <option value="operator_admin">Operator admin</option>
        </SelectField>
        <TextField
          label="Reason"
          name="reason"
          hint={REASON_HINT}
          maxLength={500}
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          error={reasonError}
        />
        <div>
          <Button type="submit" pending={m.isPending} className="max-lg:h-(--vs-control-touch)">
            {m.isPending ? "Adding" : "Add operator"}
          </Button>
        </div>
        <FormMessage error={m.error} />
      </form>
    </section>
  );
}
