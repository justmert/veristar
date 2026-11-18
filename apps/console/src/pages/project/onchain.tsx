// the on-chain page: declare the project's accounts and contracts, prove
// control of each, see their ledger activity over a range, and finish the
// two-way stellar.toml link. every number comes from the daily rows the
// chain activity job stored (contract 15); a day without a row is missing
// with its reason, never zero.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Blocks, Check, Download, MoreHorizontal, Plus, Trash2, X } from "lucide-react";
import { type FormEvent, Fragment, type ReactNode, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { ApiError } from "@/api/client";
import {
  type ChainChallenge,
  type ChainId,
  exportPaths,
  type LinkSite,
  type OnchainView,
  onchainApi,
  onchainKeys,
  onchainQuery,
  type RangeParams,
} from "@/api/onchain";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { CopyButton } from "@/components/copy-button";
import { FormMessage } from "@/components/error-panel";
import { TextField } from "@/components/form-field";
import {
  DayStrip,
  IdTotals,
  KindLabel,
  LinkStatus,
  MissingDays,
  StrKey,
  VerifiedPill,
  WeeklyActiveList,
} from "@/components/onchain";
import { ContractFunctionsBlock } from "@/components/onchain-functions";
import { PageHeader } from "@/components/page-header";
import { type CustomRange, RangeControl } from "@/components/range-control";
import { StatCard } from "@/components/stat-card";
import { EmptyState, ErrorState } from "@/components/states";
import { Pill } from "@/components/status";
import { toast } from "@/components/toast";
import { Button } from "@/components/ui/button";
import { Menu, MenuContent, MenuItem, MenuTrigger } from "@/components/ui/menu";
import { WalletPicker } from "@/components/wallet-picker";
import { countOf, formatDateTimeUtc } from "@/lib/format";
import { isDay, lastDays } from "@/lib/global";
import {
  DAYS_WITHOUT_DATA,
  METHOD_TEXT,
  shortKey,
  verifiedActivity,
  verifyErrorText,
  weeklyActiveText,
} from "@/lib/onchain";
import { connectWallet, signMessage, WalletError } from "@/lib/stellar-wallet";
import { useProject } from "@/pages/project-layout";
import { OnchainSkeleton, PAGE_HEADERS } from "./page-frame";

const CADENCE = "Veristar reads the ledger once a day.";

// the on-chain views cover at most this many days (contract 15)
const MAX_DAYS = 366;

type Preset = "7" | "30" | "90" | "365";
const PRESETS: readonly { value: Preset; label: string }[] = [
  { value: "7", label: "7 days" },
  { value: "30", label: "30 days" },
  { value: "90", label: "90 days" },
  { value: "365", label: "365 days" },
];

type RangeState = { preset: Preset | "custom"; custom: CustomRange | null };

// the range lives in the address, so a view can be shared and reloaded
export function readOnchainRange(params: URLSearchParams): RangeState {
  const r = params.get("range");
  const from = params.get("from") ?? "";
  const to = params.get("to") ?? "";
  if (r === "custom" && isDay(from) && isDay(to) && from <= to)
    return { preset: "custom", custom: { from, to } };
  const preset = PRESETS.find((p) => p.value === r)?.value ?? "30";
  return { preset, custom: null };
}

// the request range: the 30 days the control plane reads by default, the
// last n utc days, or the custom days
export function onchainRequest(s: RangeState, now: Date): RangeParams {
  if (s.preset === "custom" && s.custom) return s.custom;
  if (s.preset === "30") return {};
  return lastDays(Number(s.preset), now);
}

export function OnchainPage() {
  const { tenant, termsCurrent, isOwner } = useProject();
  const canChange = termsCurrent && tenant.status !== "suspended";
  const [params, setParams] = useSearchParams();
  const range = readOnchainRange(params);
  const view = useQuery({
    ...onchainQuery(tenant.id, onchainRequest(range, new Date())),
    placeholderData: (prev) => prev,
  });
  const v = view.data;

  function setRange(next: RangeState) {
    const p = new URLSearchParams(params);
    p.delete("from");
    p.delete("to");
    if (next.preset === "30") p.delete("range");
    else p.set("range", next.preset);
    if (next.preset === "custom" && next.custom) {
      p.set("from", next.custom.from);
      p.set("to", next.custom.to);
    }
    setParams(p, { replace: true });
  }

  const header = (
    <PageHeader
      {...PAGE_HEADERS.onchain}
      freshness={v ? freshnessOf(v) : null}
      secondary={
        v && v.ids.length > 0 ? (
          <Button asChild variant="outline">
            <a href={exportPaths.overview(tenant.id, { from: v.from, to: v.to })} download>
              <Download aria-hidden />
              Export CSV
            </a>
          </Button>
        ) : null
      }
    />
  );

  let body: ReactNode;
  if (view.isPending) body = <OnchainSkeleton />;
  else if (view.error || !v)
    body = (
      <ErrorState
        title="On-chain activity did not load"
        error={view.error}
        onRetry={() => view.refetch()}
      />
    );
  else
    body = (
      <>
        {v.ids.length > 0 ? (
          <>
            <RangeControl<Preset>
              value={range.preset}
              presets={PRESETS}
              onPreset={(preset) => setRange({ preset, custom: null })}
              custom={range.custom}
              onCustom={(custom) => setRange({ preset: "custom", custom })}
              range={{ from: v.from, to: v.to }}
              maxDays={MAX_DAYS}
              maxDay={lastDays(1, new Date()).to}
            />
            <Activity view={v} />
          </>
        ) : null}
        <Ids view={v} tenantId={tenant.id} canChange={canChange} isOwner={isOwner} />
        <LinkSection view={v} tenantId={tenant.id} />
        <p className="max-w-prose text-caption text-fg-2">
          Ledger data is public. Veristar keeps daily counts per id and per contract function. It
          keeps no counterparty, caller or transaction.
        </p>
      </>
    );

  return (
    <div className="flex min-w-0 flex-col gap-8">
      {header}
      {body}
    </div>
  );
}

// the latest successful ledger read of any id
function freshnessOf(v: OnchainView) {
  const reads = v.ids.map((id) => id.read.last_success_at).filter((t): t is string => !!t);
  if (reads.length === 0) return null;
  return { at: reads.reduce((a, b) => (a > b ? a : b)), cadence: CADENCE };
}

// ---- activity: the numbers as scf reviewers count them

function Activity({ view }: { view: OnchainView }) {
  const a = verifiedActivity(view);
  const w = view.totals.weekly_active_accounts;
  const none = a.verified === 0 ? "No verified account or contract" : undefined;
  return (
    <section aria-labelledby="activity-h" className="flex min-w-0 flex-col gap-4">
      <div className="flex flex-col gap-1">
        <h2 id="activity-h" className="text-section text-fg">
          Activity
        </h2>
        <p className="text-small text-fg-2" data-testid="activity-scope">
          {a.verified} of{" "}
          {countOf(view.totals.ids, "account or contract", "accounts and contracts")} verified. Only
          verified ones count, as SCF reviewers see them.
        </p>
      </div>
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <StatCard
          data-stat="operations"
          label="Operations"
          definition="Successful operations that involve your verified accounts and contracts."
          value={none ? null : a.operations}
          missingReason={none}
        />
        <StatCard
          data-stat="transactions"
          label="Transactions"
          definition="Transactions that hold those operations."
          value={none ? null : a.transactions}
          missingReason={none}
        />
        <StatCard
          data-stat="waa"
          label="Weekly active accounts"
          definition="The average of the counted ISO weeks. An active account started at least one operation with your verified ids in the week."
          value={w.average === null ? null : Math.round(w.average)}
          missingReason={weeklyActiveText(w)}
        />
        <StatCard
          data-stat="days-missing"
          label="Days without data"
          definition={DAYS_WITHOUT_DATA}
          value={none ? null : a.daysMissing}
          missingReason={none}
        />
      </div>
      <div className="flex min-w-0 flex-col gap-2 rounded-lg border border-line bg-raised p-4 sm:p-5">
        <h3 className="text-caption text-fg-2">Daily operations, UTC days</h3>
        <DayStrip label="Daily operations of the project" points={a.daily} />
        <p className="text-caption text-fg-2">
          A dashed day has no data. Point at it for the reason.
        </p>
      </div>
      <div className="flex min-w-0 flex-col gap-3 rounded-lg border border-line bg-raised p-4 sm:p-5">
        <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
          <h3 className="text-title text-fg">Weekly active accounts</h3>
          <span className="text-small text-fg-2" data-testid="waa-summary">
            {weeklyActiveText(w)}
          </span>
        </div>
        <p className="max-w-prose text-caption text-fg-2">
          Accounts that started at least one operation with your verified ids in an ISO week (UTC).
          This is the SCF Growth Hack definition.
        </p>
        <WeeklyActiveList weeks={view.weeks} label="Weekly active accounts per ISO week" />
      </div>
    </section>
  );
}

// ---- accounts and contracts

// refetches the project's on-chain views after a change
function useRefresh(tenantId: string) {
  const qc = useQueryClient();
  return () => qc.invalidateQueries({ queryKey: onchainKeys.all(tenantId) });
}

function Ids({
  view,
  tenantId,
  canChange,
  isOwner,
}: {
  view: OnchainView;
  tenantId: string;
  canChange: boolean;
  isOwner: boolean;
}) {
  return (
    <section aria-labelledby="ids-h" className="flex min-w-0 flex-col gap-4">
      <div className="flex flex-col gap-1">
        <h2 id="ids-h" className="text-section text-fg">
          Accounts and contracts
        </h2>
        <p className="text-small text-fg-2">
          Prove control of each id. Only proven ids count toward your activity.
        </p>
      </div>
      {canChange ? <AddForm tenantId={tenantId} /> : null}
      {view.ids.length === 0 ? (
        <EmptyState
          icon={<Blocks aria-hidden strokeWidth={1.75} />}
          title="No accounts or contracts"
        >
          {canChange
            ? "Add an account (G…) or a contract (C…). Ids from your enrollment show here."
            : "An owner or member adds them after the terms are accepted."}
        </EmptyState>
      ) : (
        <ul className="flex flex-col gap-3" aria-label="Declared ids">
          {view.ids.map((id) => (
            <IdItem
              key={id.id}
              id={id}
              tenantId={tenantId}
              range={{ from: view.from, to: view.to }}
              canChange={canChange}
              isOwner={isOwner}
            />
          ))}
        </ul>
      )}
    </section>
  );
}

function AddForm({ tenantId }: { tenantId: string }) {
  const refresh = useRefresh(tenantId);
  const [strkey, setStrkey] = useState("");
  const [error, setError] = useState<string | null>(null);
  const add = useMutation({
    mutationFn: (s: string) => onchainApi.add(tenantId, s),
    onSuccess: async (id) => {
      setStrkey("");
      toast.success(
        `${id.kind === "account" ? "Account" : "Contract"} ${shortKey(id.strkey)} added.`,
      );
      await refresh();
    },
    onError: (err) => {
      if (err instanceof ApiError && err.fields.strkey) {
        setError(
          err.fields.strkey === "duplicate"
            ? "The project already declares this id."
            : err.fields.strkey === "too_many"
              ? "A project declares at most 20 accounts and contracts."
              : "Enter a Stellar account (G…) or a Soroban contract (C…). Add a muxed account (M…) as its G account.",
        );
        return;
      }
      setError(null);
    },
  });
  function submit(e: FormEvent) {
    e.preventDefault();
    const s = strkey.trim();
    if (!/^[GC][A-Z2-7]{55}$/.test(s)) {
      setError("Enter a Stellar account (G…) or a Soroban contract (C…), 56 characters.");
      return;
    }
    setError(null);
    add.mutate(s);
  }
  return (
    <form
      onSubmit={submit}
      noValidate
      aria-label="Add an account or contract"
      className="flex flex-col gap-3 rounded-lg border border-line bg-raised p-4 sm:p-5"
    >
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start">
        <div className="min-w-0 flex-1">
          <TextField
            label="Account or contract"
            name="strkey"
            value={strkey}
            onChange={(e) => setStrkey(e.target.value)}
            placeholder="G… or C…"
            autoComplete="off"
            spellCheck={false}
            className="font-mono"
            error={error ?? undefined}
          />
        </div>
        <Button
          type="submit"
          pending={add.isPending}
          className="sm:mt-6 max-lg:h-(--vs-control-touch)"
        >
          <Plus aria-hidden />
          {add.isPending ? "Adding" : "Add"}
        </Button>
      </div>
      {add.error && !(add.error instanceof ApiError && add.error.fields.strkey) ? (
        <FormMessage error={add.error} />
      ) : null}
    </form>
  );
}

function IdItem({
  id,
  tenantId,
  range,
  canChange,
  isOwner,
}: {
  id: ChainId;
  tenantId: string;
  range: { from: string; to: string };
  canChange: boolean;
  isOwner: boolean;
}) {
  const refresh = useRefresh(tenantId);
  const [confirm, setConfirm] = useState(false);
  const kind = id.kind === "account" ? "account" : "contract";
  const remove = useMutation({
    mutationFn: () => onchainApi.remove(tenantId, id.id),
    onSuccess: async () => {
      setConfirm(false);
      toast.success(
        `${kind === "account" ? "Account" : "Contract"} ${shortKey(id.strkey)} removed.`,
      );
      await refresh();
    },
  });
  const v = id.verification;
  const verified = v.status === "verified";
  return (
    <li
      data-id={id.strkey}
      className="flex min-w-0 flex-col gap-4 rounded-lg border border-line bg-raised p-4 sm:p-5"
    >
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <KindLabel kind={id.kind} />
        <StrKey strkey={id.strkey} />
        <CopyButton
          text={id.strkey}
          label={`Copy the ${kind} id`}
          iconOnly
          size="xs"
          variant="ghost"
        />
        <VerifiedPill id={id} />
        {id.added_by === "enrollment" ? (
          <span className="text-caption text-fg-2">From enrollment</span>
        ) : null}
        {canChange && isOwner ? (
          <span className="ml-auto">
            <Menu>
              <MenuTrigger asChild>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label={`Actions for ${id.strkey}`}
                  className="max-lg:size-(--vs-control-touch)"
                >
                  <MoreHorizontal aria-hidden />
                </Button>
              </MenuTrigger>
              <MenuContent align="end">
                <MenuItem
                  tone="critical"
                  onSelect={() => {
                    remove.reset();
                    setConfirm(true);
                  }}
                >
                  <Trash2 aria-hidden />
                  Remove {kind}
                </MenuItem>
              </MenuContent>
            </Menu>
          </span>
        ) : null}
      </div>

      {verified ? (
        <p className="text-small text-fg-2">
          Proven by {v.method ? METHOD_TEXT[v.method].toLowerCase() : "a proof"}
          {v.via ? ` through ${shortKey(v.via)}` : ""}
          {v.verified_at ? ` on ${formatDateTimeUtc(v.verified_at)}` : ""}.
        </p>
      ) : null}
      {v.last_error ? (
        <p className="text-small text-fg" data-last-error={v.last_error}>
          <span className="text-warning">Last check:</span> {verifyErrorText(v.last_error)}
        </p>
      ) : null}
      {canChange ? (
        id.kind === "account" ? (
          <AccountProof id={id} tenantId={tenantId} />
        ) : (
          <ContractProof id={id} tenantId={tenantId} />
        )
      ) : null}

      <div className="flex min-w-0 flex-col gap-2">
        <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
          <IdTotals id={id} />
          {id.read.home_domain ? (
            <span className="text-caption text-fg-2">Home domain {id.read.home_domain}</span>
          ) : null}
        </div>
        <DayStrip
          label={`Daily operations of ${id.strkey}`}
          points={id.series.map((p) => ({ day: p.day, value: p.operations, missing: p.missing }))}
        />
        <MissingDays id={id} />
      </div>
      <ContractFunctionsBlock id={id} source={{ tenantId, range }} />
      <ConfirmDialog
        open={confirm}
        onOpenChange={setConfirm}
        title={`Remove ${kind} ${shortKey(id.strkey)}`}
        description="Its activity no longer shows for the project. Add it again to bring its history back."
        confirmLabel={`Remove ${kind}`}
        pendingLabel="Removing"
        pending={remove.isPending}
        error={remove.error}
        onConfirm={() => remove.mutate()}
      />
    </li>
  );
}

// an account is proven by a sep-53 signed message from a wallet, or by a
// data entry the account publishes with a manage-data operation
function AccountProof({ id, tenantId }: { id: ChainId; tenantId: string }) {
  const refresh = useRefresh(tenantId);
  const [challenge, setChallenge] = useState<ChainChallenge | null>(null);
  const [picker, setPicker] = useState(false);
  const verified = id.verification.status === "verified";
  const create = useMutation({
    mutationFn: () => onchainApi.challenge(tenantId, id.id),
    onSuccess: (c) => setChallenge(c),
  });
  const check = useMutation({
    mutationFn: (body: { method: "sep53" | "manage_data"; signature?: string }) =>
      onchainApi.verify(tenantId, id.id, body),
    onSuccess: async (r) => {
      if (r.verification.status === "verified" && !r.verification.last_error) {
        setChallenge(null);
        toast.success(`Account ${shortKey(id.strkey)} verified.`);
      }
      await refresh();
    },
  });
  if (!challenge) {
    return (
      <div className="flex flex-wrap items-center gap-2">
        <Button
          variant={verified ? "ghost" : "outline"}
          size="sm"
          onClick={() => create.mutate()}
          pending={create.isPending}
          className="max-lg:h-(--vs-control-touch)"
        >
          {verified ? "Prove again" : "Prove control"}
        </Button>
        <FormMessage error={create.error} />
      </div>
    );
  }
  return (
    <div className="flex animate-rise-in flex-col gap-4 rounded-md border border-line bg-sunken p-4">
      <p className="text-small text-fg-2">
        Prove that the project controls this account before{" "}
        {formatDateTimeUtc(challenge.expires_at)}. Use one of the two methods.
      </p>
      <ProofStep n="1" title="Sign with a wallet">
        <p className="text-small text-fg-2">
          The wallet signs this message. Signing moves no funds and sends no transaction.
        </p>
        <pre className="overflow-x-auto rounded-md border border-line bg-raised p-2 font-mono text-caption whitespace-pre-wrap break-all text-fg-2">
          {challenge.message}
        </pre>
        <Button
          size="sm"
          variant="outline"
          className="w-fit max-lg:h-(--vs-control-touch)"
          onClick={() => setPicker(true)}
        >
          Sign with a wallet
        </Button>
      </ProofStep>
      <ProofStep n="2" title="Or publish a data entry">
        <p className="text-small text-fg-2">
          Send a manage data operation from the account with this name and value. Then check it. You
          can delete the entry after the check.
        </p>
        <dl className="grid gap-3 sm:grid-cols-2">
          <div className="flex min-w-0 flex-col gap-1">
            <dt className="text-caption text-fg-2">Entry name</dt>
            <dd className="min-w-0">
              <CopyBox label="the entry name" value={challenge.manage_data.name} />
            </dd>
          </div>
          <div className="flex min-w-0 flex-col gap-1">
            <dt className="text-caption text-fg-2">Entry value</dt>
            <dd className="min-w-0">
              <CopyBox label="the entry value" value={challenge.manage_data.value} />
            </dd>
          </div>
        </dl>
        <Button
          size="sm"
          variant="outline"
          className="w-fit max-lg:h-(--vs-control-touch)"
          pending={check.isPending}
          onClick={() => check.mutate({ method: "manage_data" })}
        >
          {check.isPending ? "Checking" : "Check the data entry"}
        </Button>
      </ProofStep>
      <FormMessage error={check.error} />
      <WalletPicker
        open={picker}
        onOpenChange={setPicker}
        title="Sign the ownership message"
        description="Choose the wallet that holds this account."
        purpose="message"
        onPick={async (wallet) => {
          const address = await connectWallet(wallet.id);
          if (address !== id.strkey) {
            throw new WalletError(
              "wrong_account",
              `The wallet's active account is ${shortKey(address)}. Switch it to ${shortKey(id.strkey)} and try again.`,
            );
          }
          const { signature } = await signMessage(wallet.id, challenge.message, { address });
          await check.mutateAsync({ method: "sep53", signature });
          setPicker(false);
        }}
      />
    </div>
  );
}

function ProofStep({ n, title, children }: { n: string; title: string; children: ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <h4 className="flex items-baseline gap-2 text-body font-medium text-fg">
        <span className="font-mono text-small text-fg-2">{n}</span>
        {title}
      </h4>
      <div className="flex min-w-0 flex-col gap-2">{children}</div>
    </div>
  );
}

// a value to copy, shown whole: an entry value is short and must be exact
function CopyBox({ value, label }: { value: string; label: string }) {
  return (
    <div className="flex min-w-0 items-center gap-1 rounded-md border border-line bg-raised pl-2">
      <code className="min-w-0 flex-1 py-1 font-mono text-small break-all text-fg">{value}</code>
      <CopyButton text={value} label={`Copy ${label}`} size="xs" variant="ghost" iconOnly />
    </div>
  );
}

// a contract is proven through a verified account of the project: as the
// admin or owner its instance storage names, or as its deployer
function ContractProof({ id, tenantId }: { id: ChainId; tenantId: string }) {
  const refresh = useRefresh(tenantId);
  const [tx, setTx] = useState("");
  const [open, setOpen] = useState(id.verification.status !== "verified");
  const check = useMutation({
    mutationFn: (body: { method: "contract_admin" | "contract_deployer"; tx_hash?: string }) =>
      onchainApi.verify(tenantId, id.id, body),
    onSuccess: async (r) => {
      if (r.verification.status === "verified" && !r.verification.last_error)
        toast.success(`Contract ${shortKey(id.strkey)} verified.`);
      await refresh();
    },
  });
  if (!open)
    return (
      <Button
        variant="ghost"
        size="sm"
        className="w-fit max-lg:h-(--vs-control-touch)"
        onClick={() => setOpen(true)}
      >
        Prove again
      </Button>
    );
  return (
    <div className="flex flex-col gap-4 rounded-md border border-line bg-sunken p-4">
      <p className="text-small text-fg-2">
        Prove the contract through an account that the project verified. Use one of the two checks.
      </p>
      <ProofStep n="1" title="The account is the contract's admin">
        <Button
          size="sm"
          variant="outline"
          className="w-fit max-lg:h-(--vs-control-touch)"
          pending={check.isPending && check.variables?.method === "contract_admin"}
          disabled={check.isPending}
          onClick={() => check.mutate({ method: "contract_admin" })}
        >
          Check the admin
        </Button>
      </ProofStep>
      <ProofStep n="2" title="Or the account deployed the contract">
        <form
          className="flex flex-col gap-3 sm:flex-row sm:items-start"
          onSubmit={(e) => {
            e.preventDefault();
            check.mutate({ method: "contract_deployer", tx_hash: tx.trim().toLowerCase() });
          }}
        >
          <div className="min-w-0 flex-1">
            <TextField
              label="Deploying transaction"
              hint="The hash of the transaction that created the contract, from the last year."
              value={tx}
              onChange={(e) => setTx(e.target.value)}
              placeholder="64 hex characters"
              spellCheck={false}
              autoComplete="off"
              className="font-mono"
            />
          </div>
          <Button
            type="submit"
            size="sm"
            variant="outline"
            className="sm:mt-6 max-lg:h-(--vs-control-touch)"
            pending={check.isPending && check.variables?.method === "contract_deployer"}
            disabled={check.isPending || tx.trim().length !== 64}
          >
            Check the deployer
          </Button>
        </form>
      </ProofStep>
      <FormMessage error={check.error} />
    </div>
  );
}

// ---- the two-way stellar.toml link: one checklist per verified site, one
// action per line that is not done

function LinkSection({ view, tenantId }: { view: OnchainView; tenantId: string }) {
  return (
    <section aria-labelledby="link-h" className="flex min-w-0 flex-col gap-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex min-w-0 flex-col gap-1">
          <h2 id="link-h" className="text-section text-fg">
            stellar.toml link
          </h2>
          <p className="max-w-prose text-small text-fg-2">
            Your domain names your accounts, and your accounts name your domain. Only a verified
            account can link.
          </p>
        </div>
        {view.ids.length > 0 ? <LinkStatus view={view} /> : null}
      </div>
      {view.link.sites.length === 0 ? (
        <EmptyState
          compact
          title="No site to link"
          action={
            <Button asChild variant="outline" className="max-lg:h-(--vs-control-touch)">
              <Link to={`/projects/${tenantId}/sites`}>Go to sites</Link>
            </Button>
          }
        >
          The link starts from a site domain. Add a site and verify its domain.
        </EmptyState>
      ) : (
        <div className="flex flex-col gap-3">
          {view.link.sites.map((s) => (
            <LinkSiteCard key={s.site_id} s={s} tenantId={tenantId} />
          ))}
        </div>
      )}
    </section>
  );
}

function LinkSiteCard({ s, tenantId }: { s: LinkSite; tenantId: string }) {
  const site = `/projects/${tenantId}/sites/${s.site_id}`;
  const org = `https://${s.domain}`;
  return (
    <section
      aria-label={`stellar.toml link of ${s.domain}`}
      data-site={s.site_id}
      className="flex min-w-0 flex-col gap-3 rounded-lg border border-line bg-raised p-4 sm:p-5"
    >
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <span className="min-w-0 truncate text-body font-medium text-fg" title={s.domain}>
          {s.domain}
        </span>
        {s.two_way ? (
          <Pill tone="success">Linked both ways</Pill>
        ) : (
          <Pill tone="info">Not linked</Pill>
        )}
      </div>
      <ul className="flex flex-col divide-y divide-line-subtle" aria-label="Checks">
        <CheckLine ok={s.verified} text="The domain is verified.">
          <ActionLink to={site}>Verify the domain</ActionLink>
        </CheckLine>
        <CheckLine
          ok={s.stellar_toml === "found"}
          text="The domain publishes a stellar.toml file."
          note={`Publish it at ${org}/.well-known/stellar.toml.`}
        >
          <ActionLink to={site}>Open site setup</ActionLink>
        </CheckLine>
        <CheckLine
          ok={s.org_url_matches}
          text="The file names this domain as your organisation website."
          note={s.org_url ? `It names ${s.org_url} now.` : "Add these lines to the file."}
        >
          <CopyButton
            text={`[DOCUMENTATION]\nORG_URL = "${org}"`}
            label="Copy the lines"
            accessibleLabel={`Copy the lines that name ${s.domain}`}
          />
        </CheckLine>
        {s.accounts.map((a) => (
          <Fragment key={a.strkey}>
            <CheckLine
              ok={a.listed_in_toml}
              text={`The file lists account ${shortKey(a.strkey)}.`}
              note="Add the account to the ACCOUNTS list of the file."
            >
              <CopyButton
                text={`ACCOUNTS = ["${a.strkey}"]`}
                label="Copy the line"
                accessibleLabel={`Copy the line that lists ${a.strkey}`}
              />
            </CheckLine>
            <CheckLine
              ok={a.home_domain_matches}
              text={`Account ${shortKey(a.strkey)} names ${s.domain} as its home domain.`}
              note={
                a.home_domain
                  ? `It names ${a.home_domain} now. Set the home domain with a set options operation.`
                  : "Set the home domain with a set options operation from the account."
              }
            >
              <CopyButton
                text={s.domain}
                label="Copy the domain"
                accessibleLabel={`Copy ${s.domain} for the home domain of ${a.strkey}`}
              />
            </CheckLine>
          </Fragment>
        ))}
      </ul>
    </section>
  );
}

// one line of the checklist: done, or not done with the one action that
// fixes it
function CheckLine({
  ok,
  text,
  note,
  children,
}: {
  ok: boolean;
  text: string;
  note?: string;
  children: ReactNode;
}) {
  return (
    <li
      data-check={ok ? "done" : "open"}
      className="flex flex-wrap items-start gap-x-3 gap-y-2 py-3 first:pt-0 last:pb-0"
    >
      <span
        className={
          ok
            ? "mt-1 flex size-4 shrink-0 items-center justify-center rounded-full bg-success-soft text-success"
            : "mt-1 flex size-4 shrink-0 items-center justify-center rounded-full border border-line-strong text-fg-2"
        }
      >
        {ok ? (
          <Check aria-hidden className="size-3" strokeWidth={2.6} />
        ) : (
          <X aria-hidden className="size-3" strokeWidth={2.6} />
        )}
      </span>
      <span className="flex min-w-0 flex-1 flex-col gap-1">
        <span className="text-small text-fg">
          {text}
          <span className="sr-only">{ok ? " Done." : " Not done."}</span>
        </span>
        {!ok && note ? <span className="text-caption text-fg-2">{note}</span> : null}
      </span>
      {ok ? null : <span className="flex shrink-0 items-center">{children}</span>}
    </li>
  );
}

function ActionLink({ to, children }: { to: string; children: ReactNode }) {
  return (
    <Button asChild variant="outline" size="sm" className="max-lg:h-(--vs-control-touch)">
      <Link to={to}>{children}</Link>
    </Button>
  );
}
