import { useMutation, useQuery } from "@tanstack/react-query";
import { CirclePause, KeyRound, UserMinus } from "lucide-react";
import { type ReactNode, useState } from "react";
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
import { CreateDialog } from "@/components/create-dialog";
import { type Column, DataTable } from "@/components/data-table";
import { SelectField, TextField } from "@/components/form-field";
import { useOperatorAdmin } from "@/components/nav/operator-role";
import { PageHeader } from "@/components/page-header";
import type { RowAction } from "@/components/row-menu";
import { ErrorState, LoadingState, Skeleton } from "@/components/states";
import { Pill } from "@/components/status";
import { toast } from "@/components/toast";
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
  const admin = useOperatorAdmin() === true;
  const error = me.error ?? roster.error;
  return (
    <div className="flex min-w-0 flex-col gap-8">
      <PageHeader title="Operators" primary={admin ? <AddOperator /> : null} />
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
        <RosterSection items={roster.data.items} admin={admin} me={me.data.user} />
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
          {e.bootstrap ? <RoleTag>Deploy settings</RoleTag> : null}
          {mine(e, me) ? <RoleTag>You</RoleTag> : null}
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
  const pick = (entry: RosterEntry) => (action: RosterAction | RosterRole) =>
    setPending({ entry, action });
  return (
    <section aria-label="Roster" className="flex min-w-0 flex-col gap-3">
      <DataTable
        label="Roster"
        columns={columns}
        rows={items}
        getRowKey={(e) => e.id}
        rowLabel={(e) => identity(e)}
        rowProps={(e) => ({ "data-roster": e.id })}
        rowActions={admin ? (e) => rosterActions(e, pick(e)) : undefined}
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

// the row menu: the everyday changes first, the destructive ones last.
// each opens the confirmation with its reason.
function rosterActions(
  entry: RosterEntry,
  onPick: (a: RosterAction | RosterRole) => void,
): (RowAction | null)[] {
  return [
    entry.bootstrap
      ? null
      : {
          label: entry.role === "operator_admin" ? "Make operator" : "Make operator admin",
          onSelect: () => onPick(entry.role === "operator_admin" ? "operator" : "operator_admin"),
        },
    entry.bootstrap || entry.status === "active"
      ? null
      : { label: "Resume", onSelect: () => onPick("resume") },
    {
      label: "Reset passkeys",
      icon: <KeyRound aria-hidden />,
      destructive: true,
      onSelect: () => onPick("reset-passkeys"),
    },
    entry.bootstrap || entry.status !== "active"
      ? null
      : {
          label: "Suspend",
          icon: <CirclePause aria-hidden />,
          destructive: true,
          onSelect: () => onPick("suspend"),
        },
    entry.bootstrap
      ? null
      : {
          label: "Remove from the roster",
          icon: <UserMinus aria-hidden />,
          destructive: true,
          onSelect: () => onPick("remove"),
        },
  ];
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

// the header button and the form in its dialog, as on the other operator
// pages
export function AddOperator() {
  const [kind, setKind] = useState<"github" | "stellar">("github");
  const [value, setValue] = useState("");
  const [role, setRole] = useState<RosterRole>("operator");
  const [reason, setReason] = useState("");
  const [errors, setErrors] = useState<{ identity?: string; reason?: string }>({});
  const [open, setOpen] = useState(false);
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
      setOpen(false);
    },
  });
  // a 422 names its fields (2.5); each is shown at its field
  const fields = m.error instanceof ApiError ? m.error.fields : {};
  const serverField = (k: string) => (fields[k] ? fieldMessage(fields[k]) : undefined);
  const identityError =
    errors.identity ?? serverField(kind === "github" ? "github_login" : "stellar_address");
  const reasonError = errors.reason ?? serverField("reason");
  function submit() {
    const v = validateAddOperator({ kind, value, reason });
    setErrors(v);
    if (v.identity || v.reason) return;
    m.mutate();
  }
  return (
    <CreateDialog
      label="Add operator"
      title="Add an operator"
      pendingLabel="Adding"
      pending={m.isPending}
      error={m.error instanceof ApiError && Object.keys(fields).length > 0 ? null : m.error}
      onSubmit={submit}
      open={open}
      onOpenChange={setOpen}
      onClosed={() => {
        setErrors({});
        m.reset();
      }}
    >
      <SelectField
        label="Signs in with"
        name="kind"
        value={kind}
        onChange={(e) => setKind(e.target.value as "github" | "stellar")}
        className="w-full"
      >
        <option value="github">GitHub</option>
        <option value="stellar">Stellar wallet or passkey</option>
      </SelectField>
      <TextField
        label={kind === "github" ? "GitHub login" : "Stellar address"}
        placeholder={kind === "github" ? undefined : "G… or C…"}
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
        className="w-full"
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
    </CreateDialog>
  );
}

// a plain tag for a fact about a person (from the deploy settings, you),
// apart from the status pills: no dot, no tint
function RoleTag({ children }: { children: ReactNode }) {
  return (
    <span className="inline-flex h-5 items-center rounded-sm border border-line px-1 text-caption text-fg-2">
      {children}
    </span>
  );
}
