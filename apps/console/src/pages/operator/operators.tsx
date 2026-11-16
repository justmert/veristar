import { useMutation, useQuery } from "@tanstack/react-query";
import { type FormEvent, useState } from "react";
import {
  operatorKeys,
  operatorsApi,
  type Passkey,
  passkeysQuery,
  type RosterAction,
  type RosterEntry,
  type RosterRole,
  rosterQuery,
} from "@/api/operators";
import { keys, meQuery } from "@/api/queries";
import type { User } from "@/api/schemas";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { ErrorPanel, FormMessage, Loading } from "@/components/error-panel";
import { SelectField, TextField } from "@/components/form-field";
import { Panel } from "@/components/panel";
import { ReasonDialog } from "@/components/reason-dialog";
import { Pill } from "@/components/status";
import { registerPasskey } from "@/components/step-up";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { formatDateTimeUtc } from "@/lib/format";
import { shortAddress } from "@/lib/stellar-wallet";
import { queryClient } from "@/query-client";

const ROLE_TEXT: Record<RosterRole, string> = {
  operator: "Operator",
  operator_admin: "Operator admin",
};

const REASON_HINT = "Recorded in the audit log with your name.";

function refresh() {
  void queryClient.invalidateQueries({ queryKey: operatorKeys.roster });
  void queryClient.invalidateQueries({ queryKey: operatorKeys.passkeys });
  void queryClient.invalidateQueries({ queryKey: keys.me });
}

// the roster rows that name the signed-in account
function mine(e: RosterEntry, u: User) {
  return (
    (e.github_id !== null && e.github_id === u.github_id) ||
    (e.stellar_address !== null && e.stellar_address === u.stellar_address)
  );
}

// the operator roster (contract 5.16) and the signed-in operator's own
// passkeys (contract 5.15). every operator sees the roster; operator admins
// change it, and every change asks for a reason that goes to the audit log
export function OperatorsPage() {
  const me = useQuery(meQuery);
  const roster = useQuery(rosterQuery);
  if (me.isPending || roster.isPending) return <Loading label="Loading the operators" />;
  if (me.error) return <ErrorPanel error={me.error} onRetry={() => me.refetch()} />;
  if (roster.error) return <ErrorPanel error={roster.error} onRetry={() => roster.refetch()} />;
  const items = roster.data.items;
  const admin = items.some(
    (e) => mine(e, me.data.user) && e.status === "active" && e.role === "operator_admin",
  );
  return (
    <div className="flex animate-rise-in flex-col gap-8">
      <header className="flex max-w-prose flex-col gap-1">
        <h2 className="text-section text-fg">Operators</h2>
        <p className="text-body text-fg-2">
          The people who hold the operator view, each with a GitHub account or a Stellar wallet.
          Operator admins add, suspend and remove operators here. The identities in the deploy
          settings are operator admins at every start of the service and cannot be changed here.
        </p>
      </header>
      <RosterSection items={items} admin={admin} me={me.data.user} />
      {admin ? <AddOperator /> : null}
      <PasskeySection />
    </div>
  );
}

function identity(e: RosterEntry) {
  if (e.kind === "github") return e.github_login ?? `GitHub id ${e.github_id}`;
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
    title: "Make an operator admin:",
    confirm: "Make admin",
    pending: "Saving",
    tone: "primary",
  },
  operator: {
    title: "Make a plain operator:",
    confirm: "Make operator",
    pending: "Saving",
    tone: "primary",
  },
};

const ACTION_DETAIL: Record<RosterAction | RosterRole, string> = {
  suspend:
    "The person loses the operator view at once, their console sessions end and their AI assistant connections with operator access are revoked.",
  resume:
    "The person holds the operator view again after their next sign-in and passkey confirmation.",
  remove:
    "The person leaves the roster. Their passkeys are deleted and their console sessions end.",
  "reset-passkeys":
    "Every passkey of the person is deleted and their sessions end. They can register a new one within the enrollment window that opens now.",
  operator_admin: "Operator admins add, suspend and remove operators.",
  operator: "The person keeps the operator view but can no longer change the roster.",
};

function RosterSection({ items, admin, me }: { items: RosterEntry[]; admin: boolean; me: User }) {
  const [pending, setPending] = useState<Pending | null>(null);
  const m = useMutation({
    mutationFn: async ({ p, reason }: { p: Pending; reason: string }): Promise<void> => {
      if (p.action === "operator" || p.action === "operator_admin")
        await operatorsApi.setRole(p.entry.id, p.action, reason);
      else await operatorsApi.rosterAction(p.entry.id, p.action, reason);
    },
    onSuccess: () => {
      setPending(null);
      refresh();
    },
  });
  const t = pending ? ACTION_TEXT[pending.action] : null;
  return (
    <Panel title="Roster" flush>
      <div className="overflow-x-auto">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Operator</TableHead>
              <TableHead>Role</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Account</TableHead>
              {admin ? <TableHead className="text-right">Actions</TableHead> : null}
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((e) => (
              <TableRow key={e.id} data-roster={e.id}>
                <TableCell>
                  <span className="flex flex-wrap items-center gap-2">
                    <span className="font-mono text-fg">{identity(e)}</span>
                    <span className="text-caption text-fg-3">
                      {e.kind === "github" ? "GitHub" : "Stellar wallet"}
                    </span>
                    {e.bootstrap ? <Pill tone="info">Deploy settings</Pill> : null}
                    {mine(e, me) ? <Pill tone="info">You</Pill> : null}
                  </span>
                </TableCell>
                <TableCell>{ROLE_TEXT[e.role]}</TableCell>
                <TableCell>
                  <span className="flex flex-col">
                    {e.status === "active" ? (
                      <Pill tone="success">Active</Pill>
                    ) : (
                      <Pill tone="warning">Suspended</Pill>
                    )}
                    {e.status_reason ? (
                      <span className="text-caption text-fg-3">{e.status_reason}</span>
                    ) : null}
                  </span>
                </TableCell>
                <TableCell>
                  <span className="flex flex-col text-small text-fg-2">
                    {e.account ? (
                      <>
                        <span>
                          {e.account.passkeys} {e.account.passkeys === 1 ? "passkey" : "passkeys"}
                        </span>
                        <span className="text-caption text-fg-3">
                          {e.account.last_login_at
                            ? `Last sign-in ${formatDateTimeUtc(e.account.last_login_at)}`
                            : "Never signed in"}
                        </span>
                      </>
                    ) : (
                      <span>No sign-in yet</span>
                    )}
                    {e.enrollment_until ? (
                      <span className="text-caption text-fg-3">
                        First passkey before {formatDateTimeUtc(e.enrollment_until)}
                      </span>
                    ) : null}
                  </span>
                </TableCell>
                {admin ? (
                  <TableCell className="text-right">
                    <RowActions entry={e} onPick={(action) => setPending({ entry: e, action })} />
                  </TableCell>
                ) : null}
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
      <ReasonDialog
        open={pending !== null}
        onOpenChange={(o) => {
          if (!o) {
            setPending(null);
            m.reset();
          }
        }}
        title={t && pending ? `${t.title} ${identity(pending.entry)}` : ""}
        description={pending ? ACTION_DETAIL[pending.action] : ""}
        reasonRequired
        hint={REASON_HINT}
        maxLength={500}
        confirmLabel={t?.confirm ?? ""}
        pendingLabel={t?.pending ?? ""}
        tone={t?.tone ?? "primary"}
        pending={m.isPending}
        error={m.error}
        onConfirm={(reason) => pending && m.mutate({ p: pending, reason })}
      />
    </Panel>
  );
}

function RowActions({
  entry,
  onPick,
}: {
  entry: RosterEntry;
  onPick: (a: RosterAction | RosterRole) => void;
}) {
  if (entry.bootstrap)
    return (
      <Button size="sm" variant="outline" onClick={() => onPick("reset-passkeys")}>
        Reset passkeys
      </Button>
    );
  return (
    <span className="inline-flex flex-wrap justify-end gap-2">
      {entry.status === "active" ? (
        <Button size="sm" variant="outline" onClick={() => onPick("suspend")}>
          Suspend
        </Button>
      ) : (
        <Button size="sm" variant="outline" onClick={() => onPick("resume")}>
          Resume
        </Button>
      )}
      <Button
        size="sm"
        variant="outline"
        onClick={() => onPick(entry.role === "operator_admin" ? "operator" : "operator_admin")}
      >
        {entry.role === "operator_admin" ? "Make operator" : "Make admin"}
      </Button>
      <Button size="sm" variant="outline" onClick={() => onPick("reset-passkeys")}>
        Reset passkeys
      </Button>
      <Button size="sm" variant="outline" onClick={() => onPick("remove")}>
        Remove
      </Button>
    </span>
  );
}

function AddOperator() {
  const [kind, setKind] = useState<"github" | "stellar">("github");
  const [value, setValue] = useState("");
  const [role, setRole] = useState<RosterRole>("operator");
  const [reason, setReason] = useState("");
  const m = useMutation({
    mutationFn: () =>
      operatorsApi.addOperator({
        ...(kind === "github" ? { github_login: value.trim() } : { stellar_address: value.trim() }),
        role,
        reason: reason.trim(),
      }),
    onSuccess: () => {
      setValue("");
      setReason("");
      refresh();
    },
  });
  function submit(e: FormEvent) {
    e.preventDefault();
    m.mutate();
  }
  return (
    <Panel
      title="Add an operator"
      description="The person signs in with this identity, then registers a passkey within the enrollment window that opens now (24 hours unless the deployment sets another length). An operator admin can reset passkeys to open a new window."
    >
      <form onSubmit={submit} className="grid max-w-prose gap-3" noValidate>
        <SelectField
          label="Signs in with"
          name="kind"
          value={kind}
          onChange={(e) => setKind(e.target.value as "github" | "stellar")}
        >
          <option value="github">GitHub</option>
          <option value="stellar">Stellar wallet</option>
        </SelectField>
        <TextField
          label={kind === "github" ? "GitHub login" : "Stellar address"}
          name="identity"
          value={value}
          onChange={(e) => setValue(e.target.value)}
          autoComplete="off"
          spellCheck={false}
          error={
            m.error && "fields" in m.error
              ? ((m.error.fields as Record<string, string>)[
                  kind === "github" ? "github_login" : "stellar_address"
                ] ?? null)
              : null
          }
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
        />
        <div>
          <Button type="submit" pending={m.isPending}>
            {m.isPending ? "Adding" : "Add operator"}
          </Button>
        </div>
        <FormMessage error={m.error} />
      </form>
    </Panel>
  );
}

function PasskeySection() {
  const list = useQuery(passkeysQuery);
  const [name, setName] = useState("");
  const [deleting, setDeleting] = useState<Passkey | null>(null);
  const add = useMutation({
    mutationFn: (n: string) => registerPasskey(n),
    onSuccess: () => {
      setName("");
      refresh();
    },
  });
  const del = useMutation({
    mutationFn: (id: string) => operatorsApi.deletePasskey(id),
    onSuccess: () => {
      setDeleting(null);
      refresh();
    },
  });
  return (
    <Panel
      title="Your passkeys"
      description="The passkeys and security keys that open the operator area for you. Keep at least two, so a lost key does not lock you out."
    >
      <div className="flex flex-col gap-4">
        {list.isPending ? (
          <Loading label="Loading your passkeys" />
        ) : list.error ? (
          <ErrorPanel error={list.error} onRetry={() => list.refetch()} />
        ) : (
          <ul className="flex flex-col divide-y divide-line">
            {list.data.items.map((p) => (
              <li
                key={p.id}
                data-passkey={p.id}
                className="flex items-center justify-between gap-3 py-2"
              >
                <span className="flex flex-col">
                  <span className="text-fg">{p.name}</span>
                  <span className="text-caption text-fg-3">
                    Added {formatDateTimeUtc(p.created_at)}
                    {p.last_used_at ? `, last used ${formatDateTimeUtc(p.last_used_at)}` : ""}
                    {p.backup_eligible ? ", synced passkey" : ", device-bound key"}
                  </span>
                </span>
                <Button size="sm" variant="outline" onClick={() => setDeleting(p)}>
                  Delete
                </Button>
              </li>
            ))}
          </ul>
        )}
        <form
          className="flex flex-wrap items-end gap-3"
          noValidate
          onSubmit={(e) => {
            e.preventDefault();
            if (name.trim()) add.mutate(name.trim());
          }}
        >
          <TextField
            label="Name of the new passkey"
            name="new-passkey"
            maxLength={64}
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
          <Button type="submit" pending={add.isPending} disabled={name.trim() === ""}>
            {add.isPending ? "Waiting for your passkey" : "Add a passkey"}
          </Button>
        </form>
        <FormMessage error={add.error} />
      </div>
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(o) => {
          if (!o) {
            setDeleting(null);
            del.reset();
          }
        }}
        title={`Delete ${deleting?.name ?? ""}`}
        description="The key no longer opens the operator area."
        confirmLabel="Delete"
        pendingLabel="Deleting"
        pending={del.isPending}
        error={del.error}
        onConfirm={() => deleting && del.mutate(deleting.id)}
      />
    </Panel>
  );
}
