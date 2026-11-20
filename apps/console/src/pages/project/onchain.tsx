// the on-chain page: declare the project's accounts and contracts, prove
// control of each, read their ledger activity over a range, and finish the
// two-way stellar.toml link. every number comes from the daily rows the
// chain activity job stored (contract 15); a day without a row is missing
// with its reason, never zero. until one id is proven the page is one
// focused card: add an id, or prove the one added.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { Blocks, Braces, Download, FileText, Plus, ShieldCheck, Trash2, User } from "lucide-react";
import { type FormEvent, type ReactNode, useEffect, useRef, useState } from "react";
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
import { type TimeBucket, TimeChart, type TimeSeries } from "@/components/charts";
import { CopyButton } from "@/components/copy-button";
import { type Column, DataTable } from "@/components/data-table";
import { FormMessage } from "@/components/error-panel";
import { TextField } from "@/components/form-field";
import {
  DayStrip,
  IdTotals,
  KindLabel,
  MissingDays,
  StrKey,
  VerifiedPill,
  WeeklyActiveList,
} from "@/components/onchain";
import { ContractFunctionsBlock, functionLabel } from "@/components/onchain-functions";
import { PageHeader } from "@/components/page-header";
import { Panel } from "@/components/panel";
import { type CustomRange, RangeControl } from "@/components/range-control";
import { type RowAction, RowMenu } from "@/components/row-menu";
import { MiniBars } from "@/components/scorecard-tile";
import { Segmented } from "@/components/segmented";
import { SetupBar, type SetupStepState } from "@/components/setup-progress";
import { countChange, StatCard, StatGrid } from "@/components/stat-card";
import { EmptyState, ErrorState, Missing } from "@/components/states";
import { Pill } from "@/components/status";
import { toast } from "@/components/toast";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Tooltip } from "@/components/ui/tooltip";
import { WalletPicker } from "@/components/wallet-picker";
import {
  countOf,
  formatCount,
  formatDate,
  formatDateRange,
  formatDateTimeUtc,
  formatTick,
  formatTimeUtc,
  formatWeekOf,
  utcDay,
} from "@/lib/format";
import { isDay, lastDays } from "@/lib/global";
import {
  DAYS_WITHOUT_DATA,
  METHOD_TEXT,
  missingText,
  shortKey,
  type VerifiedActivity,
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
// a project declares at most this many accounts and contracts
const MAX_IDS = 20;

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

const DAY_MS = 86_400_000;
const shiftDay = (day: string, by: number) =>
  new Date(Date.parse(`${day}T00:00:00Z`) + by * DAY_MS).toISOString().slice(0, 10);

// the range of the same length right before a view's range: the change of
// each figure is read against it
export function previousRange(v: Pick<OnchainView, "from" | "days">): RangeParams {
  const n = Math.max(1, v.days.length);
  return { from: shiftDay(v.from, -n), to: shiftDay(v.from, -1) };
}

export function OnchainPage() {
  const { tenant, termsCurrent, isOwner } = useProject();
  const canChange = termsCurrent && tenant.status !== "suspended";
  const [params, setParams] = useSearchParams();
  const range = readOnchainRange(params);
  const [adding, setAdding] = useState(false);
  const view = useQuery({
    ...onchainQuery(tenant.id, onchainRequest(range, new Date())),
    placeholderData: (prev) => prev,
  });
  const v = view.data;
  // until one id is proven nothing counts, so the page is its first step
  const proven = v ? v.ids.some((id) => id.verification.status === "verified") : false;

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
      freshness={v && proven ? freshnessOf(v) : undefined}
      items={v && proven ? [<span key="range">{formatDateRange(v.from, v.to)}, UTC</span>] : []}
      primary={
        v && proven && canChange ? (
          <Button onClick={() => setAdding(true)} disabled={v.ids.length >= MAX_IDS}>
            <Plus aria-hidden />
            Add account or contract
          </Button>
        ) : null
      }
      secondary={
        v && proven ? (
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
              // the range in words is the header's meta item
              words={false}
            />
            <Button asChild variant="outline">
              <a href={exportPaths.overview(tenant.id, { from: v.from, to: v.to })} download>
                <Download aria-hidden />
                Export CSV
              </a>
            </Button>
          </>
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
  else if (!proven)
    body = (
      <FirstStep
        view={v}
        tenantId={tenant.id}
        canChange={canChange}
        isOwner={isOwner}
        onAdd={() => setAdding(true)}
      />
    );
  else
    body = (
      <>
        <Activity view={v} tenantId={tenant.id} />
        <Ids view={v} tenantId={tenant.id} canChange={canChange} isOwner={isOwner} />
        <LinkSection view={v} tenantId={tenant.id} />
      </>
    );

  return (
    <div className="flex min-w-0 flex-col gap-4">
      {header}
      {body}
      <AddDialog tenantId={tenant.id} open={adding} onOpenChange={setAdding} />
    </div>
  );
}

// the latest successful ledger read of any id
function freshnessOf(v: OnchainView) {
  const reads = v.ids.map((id) => id.read.last_success_at).filter((t): t is string => !!t);
  if (reads.length === 0) return null;
  return { at: reads.reduce((a, b) => (a > b ? a : b)), cadence: CADENCE };
}

// refetches the project's on-chain views after a change
function useRefresh(tenantId: string) {
  const qc = useQueryClient();
  return () => qc.invalidateQueries({ queryKey: onchainKeys.all(tenantId) });
}

// remove an id, behind a confirmation
function removeAction(id: ChainId, tenantId: string, refresh: () => Promise<unknown>): RowAction {
  const kind = id.kind === "account" ? "account" : "contract";
  return {
    label: `Remove ${kind}`,
    icon: <Trash2 />,
    destructive: true,
    confirm: {
      title: `Remove ${kind} ${shortKey(id.strkey)}`,
      description:
        "Its activity no longer shows for the project. Add it again to bring its history back.",
      confirmLabel: `Remove ${kind}`,
      pendingLabel: "Removing",
      run: async () => {
        await onchainApi.remove(tenantId, id.id);
        toast.success(
          `${kind === "account" ? "Account" : "Contract"} ${shortKey(id.strkey)} removed.`,
        );
        await refresh();
      },
    },
  };
}

function KindIcon({ kind }: { kind: ChainId["kind"] }) {
  const Icon = kind === "account" ? User : Braces;
  return (
    <span
      aria-hidden
      className="flex size-[34px] shrink-0 items-center justify-center rounded-[10px] border border-line-strong text-fg-2"
    >
      <Icon className="size-4" strokeWidth={1.75} />
    </span>
  );
}

// ---- the first step: one focused card in the middle of the page

function FocusCard({
  children,
  ...rest
}: {
  children: ReactNode;
  [k: `data-${string}`]: string | undefined;
}) {
  return (
    <section
      {...rest}
      className="mx-auto flex w-full max-w-[640px] min-w-0 flex-col gap-5 rounded-2xl border border-line bg-raised p-7 max-sm:p-4"
    >
      {children}
    </section>
  );
}

function FirstStep({
  view,
  tenantId,
  canChange,
  isOwner,
  onAdd,
}: {
  view: OnchainView;
  tenantId: string;
  canChange: boolean;
  isOwner: boolean;
  onAdd: () => void;
}) {
  const refresh = useRefresh(tenantId);
  if (!canChange)
    return view.ids.length === 0 ? (
      <EmptyState icon={<Blocks aria-hidden strokeWidth={1.75} />} title="No accounts or contracts">
        An owner or member adds them after the terms are accepted.
      </EmptyState>
    ) : (
      <EmptyState icon={<Blocks aria-hidden strokeWidth={1.75} />} title="No ID proven yet">
        An owner or member proves control after the terms are accepted.
      </EmptyState>
    );
  return (
    <div
      data-first-step=""
      className="flex min-h-[calc(100dvh-10rem)] flex-col justify-center gap-4 py-8"
    >
      {view.ids.length === 0 ? (
        <FocusCard data-step="add">
          <h2 id="add-h" className="text-[17px] leading-6 font-semibold text-fg">
            Add a Stellar account or contract
          </h2>
          <AddForm tenantId={tenantId} labelledBy="add-h" />
        </FocusCard>
      ) : (
        view.ids.map((id) => (
          <FocusCard key={id.id} data-step="prove" data-id={id.strkey}>
            <ProveCard
              id={id}
              tenantId={tenantId}
              menu={
                <RowMenu
                  name={id.strkey}
                  actions={[
                    view.ids.length < MAX_IDS
                      ? {
                          label: "Add another account or contract",
                          icon: <Plus />,
                          onSelect: onAdd,
                        }
                      : null,
                    isOwner ? removeAction(id, tenantId, refresh) : null,
                  ]}
                />
              }
            />
          </FocusCard>
        ))
      )}
    </div>
  );
}

function AddDialog({
  tenantId,
  open,
  onOpenChange,
}: {
  tenantId: string;
  open: boolean;
  onOpenChange: (o: boolean) => void;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange} title="Add an account or contract" size="sm">
      {open ? <AddForm tenantId={tenantId} onAdded={() => onOpenChange(false)} /> : null}
    </Dialog>
  );
}

function AddForm({
  tenantId,
  onAdded,
  labelledBy,
}: {
  tenantId: string;
  onAdded?: () => void;
  labelledBy?: string;
}) {
  const refresh = useRefresh(tenantId);
  const [strkey, setStrkey] = useState("");
  const [error, setError] = useState<string | null>(null);
  const add = useMutation({
    mutationFn: (s: string) => onchainApi.add(tenantId, s),
    onSuccess: async (id) => {
      setStrkey("");
      onAdded?.();
      toast.success(
        `${id.kind === "account" ? "Account" : "Contract"} ${shortKey(id.strkey)} added.`,
      );
      await refresh();
    },
    onError: (err) => {
      if (err instanceof ApiError && err.fields.strkey) {
        setError(
          err.fields.strkey === "duplicate"
            ? "The project already declares this ID."
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
      aria-label={labelledBy ? undefined : "Add an account or contract"}
      aria-labelledby={labelledBy}
      className="flex min-w-0 flex-col gap-3"
    >
      <div className="flex flex-col gap-2 sm:flex-row sm:items-start">
        <div className="min-w-0 flex-1">
          <TextField
            label="Account or contract"
            labelHidden={Boolean(labelledBy)}
            name="strkey"
            data-autofocus=""
            value={strkey}
            onChange={(e) => setStrkey(e.target.value)}
            placeholder="G… or C…"
            autoComplete="off"
            spellCheck={false}
            className={cn("font-mono", labelledBy && "h-11")}
            hint="A muxed M… account is added as its G account. Up to 20."
            error={error ?? undefined}
          />
        </div>
        <Button
          type="submit"
          pending={add.isPending}
          className={cn(labelledBy ? "h-11 px-5" : "sm:mt-6 max-lg:h-(--vs-control-touch)")}
        >
          {add.isPending ? "Adding" : "Add"}
        </Button>
      </div>
      {add.error && !(add.error instanceof ApiError && add.error.fields.strkey) ? (
        <FormMessage error={add.error} />
      ) : null}
    </form>
  );
}

// ---- prove control: one card, the id on top, a switch between the two
// ways to prove it and the one action of the chosen way

function ProveCard({
  id,
  tenantId,
  menu,
  onDone,
}: {
  id: ChainId;
  tenantId: string;
  menu?: ReactNode;
  onDone?: () => void;
}) {
  return id.kind === "account" ? (
    <AccountProof id={id} tenantId={tenantId} menu={menu} onDone={onDone} />
  ) : (
    <ContractProof id={id} tenantId={tenantId} menu={menu} onDone={onDone} />
  );
}

function ProveHead({ id, until, menu }: { id: ChainId; until?: string | null; menu?: ReactNode }) {
  const err = id.verification.last_error;
  return (
    <div className="flex min-w-0 flex-col gap-3">
      <div className="flex min-w-0 items-start gap-3.5">
        <KindIcon kind={id.kind} />
        <div className="flex min-w-0 flex-1 flex-col gap-0.5">
          <StrKey strkey={id.strkey} className="[&_code]:text-[15px]" />
          <KindLabel kind={id.kind} />
          {until ? (
            <span className="text-caption text-fg-2">Proof open until {formatTimeUtc(until)}</span>
          ) : null}
        </div>
        {menu}
      </div>
      {err ? (
        <p className="text-small text-fg" data-last-error={err}>
          <span className="text-warning">Last check:</span> {verifyErrorText(err)}
        </p>
      ) : null}
    </div>
  );
}

// a value to copy, shown whole: an entry value is short and must be exact
function CopyBox({ value, label }: { value: string; label: string }) {
  return (
    <div className="flex min-w-0 items-center gap-1 rounded-[10px] border border-line bg-sunken pl-3">
      <code className="min-w-0 flex-1 py-2 font-mono text-small break-all text-fg">{value}</code>
      <CopyButton text={value} label={`Copy ${label}`} size="xs" variant="ghost" iconOnly />
    </div>
  );
}

type AccountWay = "sep53" | "manage_data";

// an account is proven by a sep-53 signed message from a wallet, or by a
// data entry the account publishes with a manage-data operation. the
// challenge is asked for when the card opens, so the message is there
function AccountProof({
  id,
  tenantId,
  menu,
  onDone,
}: {
  id: ChainId;
  tenantId: string;
  menu?: ReactNode;
  onDone?: () => void;
}) {
  const refresh = useRefresh(tenantId);
  const [challenge, setChallenge] = useState<ChainChallenge | null>(null);
  const [way, setWay] = useState<AccountWay>("sep53");
  const [picker, setPicker] = useState(false);
  const create = useMutation({
    mutationFn: () => onchainApi.challenge(tenantId, id.id),
    onSuccess: (c) => setChallenge(c),
  });
  // one challenge per card, also when an effect runs twice
  const asked = useRef(false);
  const ask = create.mutate;
  useEffect(() => {
    if (asked.current) return;
    asked.current = true;
    ask();
  }, [ask]);
  const check = useMutation({
    mutationFn: (body: { method: AccountWay; signature?: string }) =>
      onchainApi.verify(tenantId, id.id, body),
    onSuccess: async (r) => {
      if (r.verification.status === "verified" && !r.verification.last_error) {
        toast.success(`Account ${shortKey(id.strkey)} verified.`);
        onDone?.();
      }
      await refresh();
    },
  });
  return (
    <div className="flex min-w-0 flex-col gap-5" data-proof="account">
      <ProveHead id={id} until={challenge?.expires_at} menu={menu} />
      <Segmented<AccountWay>
        label="Way to prove control"
        value={way}
        options={[
          { value: "sep53", label: "Sign with a wallet" },
          { value: "manage_data", label: "Publish a data entry" },
        ]}
        onChange={setWay}
        className="w-full [&>*]:flex-1"
      />
      {!challenge ? (
        create.error ? (
          <div className="flex flex-wrap items-center gap-2">
            <FormMessage error={create.error} />
            <Button variant="outline" size="sm" onClick={() => create.mutate()}>
              Try again
            </Button>
          </div>
        ) : (
          <p className="text-small text-fg-2" aria-busy="true">
            Preparing the proof
          </p>
        )
      ) : way === "sep53" ? (
        <>
          <div className="relative min-w-0">
            <pre className="min-w-0 overflow-x-auto rounded-[10px] border border-line bg-sunken px-3.5 py-3 pr-10 font-mono text-caption leading-5 whitespace-pre-wrap break-all text-fg-2">
              {challenge.message}
            </pre>
            <span className="absolute top-1.5 right-1.5">
              <CopyButton
                text={challenge.message}
                label="Copy the message"
                size="xs"
                variant="ghost"
                iconOnly
              />
            </span>
          </div>
          <Button size="lg" className="w-full" onClick={() => setPicker(true)}>
            Connect a wallet and sign
          </Button>
        </>
      ) : (
        <>
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
            size="lg"
            className="w-full"
            pending={check.isPending}
            onClick={() => check.mutate({ method: "manage_data" })}
          >
            {check.isPending ? "Checking" : "Check the data entry"}
          </Button>
        </>
      )}
      <FormMessage error={check.error} />
      {challenge ? (
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
      ) : null}
    </div>
  );
}

type ContractWay = "contract_admin" | "contract_deployer";

// a contract is proven through a verified account of the project: as the
// admin or owner its instance storage names, or as its deployer
function ContractProof({
  id,
  tenantId,
  menu,
  onDone,
}: {
  id: ChainId;
  tenantId: string;
  menu?: ReactNode;
  onDone?: () => void;
}) {
  const refresh = useRefresh(tenantId);
  const [tx, setTx] = useState("");
  const [way, setWay] = useState<ContractWay>("contract_admin");
  const check = useMutation({
    mutationFn: (body: { method: ContractWay; tx_hash?: string }) =>
      onchainApi.verify(tenantId, id.id, body),
    onSuccess: async (r) => {
      if (r.verification.status === "verified" && !r.verification.last_error) {
        toast.success(`Contract ${shortKey(id.strkey)} verified.`);
        onDone?.();
      }
      await refresh();
    },
  });
  return (
    <div className="flex min-w-0 flex-col gap-5" data-proof="contract">
      <ProveHead id={id} menu={menu} />
      <Segmented<ContractWay>
        label="Way to prove control"
        value={way}
        options={[
          { value: "contract_admin", label: "The account is its admin" },
          { value: "contract_deployer", label: "The account deployed it" },
        ]}
        onChange={setWay}
        className="w-full [&>*]:flex-1"
      />
      {way === "contract_admin" ? (
        <Button
          size="lg"
          className="w-full"
          pending={check.isPending}
          onClick={() => check.mutate({ method: "contract_admin" })}
        >
          Check the admin
        </Button>
      ) : (
        <form
          className="flex flex-col gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            check.mutate({ method: "contract_deployer", tx_hash: tx.trim().toLowerCase() });
          }}
        >
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
          <Button
            type="submit"
            size="lg"
            className="w-full"
            pending={check.isPending}
            disabled={check.isPending || tx.trim().length !== 64}
          >
            Check the deployer
          </Button>
        </form>
      )}
      <FormMessage error={check.error} />
    </div>
  );
}

// the proof card of a listed id, in a dialog
function ProveDialog({
  id,
  tenantId,
  onOpenChange,
}: {
  id: ChainId | null;
  tenantId: string;
  onOpenChange: (o: boolean) => void;
}) {
  return (
    <Dialog
      open={id !== null}
      onOpenChange={onOpenChange}
      title={id?.verification.status === "verified" ? "Prove control again" : "Prove control"}
    >
      {id ? <ProveCard id={id} tenantId={tenantId} onDone={() => onOpenChange(false)} /> : null}
    </Dialog>
  );
}

// ---- activity: the figures as scf reviewers count them, and one chart

type Metric = "operations" | "transactions" | "waa";

const DEFINITION: Record<Metric, string> = {
  operations: "Successful operations that involve your verified accounts and contracts.",
  transactions: "Transactions that hold those operations.",
  waa: "The average of the counted ISO weeks. An active account started at least one operation with your verified IDs in the week. This is the SCF Growth Hack definition.",
};

function Activity({ view, tenantId }: { view: OnchainView; tenantId: string }) {
  const [metric, setMetric] = useState<Metric>("operations");
  const a = verifiedActivity(view);
  // the same number of days right before the range, for the changes
  const before = useQuery(onchainQuery(tenantId, previousRange(view)));
  const p = before.data ? verifiedActivity(before.data) : null;
  const w = view.totals.weekly_active_accounts;
  const pw = before.data?.totals.weekly_active_accounts.average ?? null;
  const gap = a.gap ?? undefined;
  const prev = p !== null && p.gap === null ? p : null;
  const period = `previous ${countOf(view.days.length, "day")}`;
  const reasons = dailyReasons(a.daily);
  return (
    <section aria-label="Activity" className="flex min-w-0 flex-col gap-4">
      <StatGrid aria-label="Figures" min="10rem" className="lg:grid-cols-4">
        <StatCard
          data-stat="operations"
          label="Operations"
          definition={DEFINITION.operations}
          value={gap ? null : a.operations}
          missingReason={gap}
          change={!gap && prev ? countChange(a.operations, prev.operations, period) : null}
          selected={metric === "operations"}
          onSelect={() => setMetric("operations")}
        />
        <StatCard
          data-stat="transactions"
          label="Transactions"
          definition={DEFINITION.transactions}
          value={gap ? null : a.transactions}
          missingReason={gap}
          change={!gap && prev ? countChange(a.transactions, prev.transactions, period) : null}
          selected={metric === "transactions"}
          onSelect={() => setMetric("transactions")}
        />
        <StatCard
          data-stat="waa"
          label="Weekly active accounts"
          definition={DEFINITION.waa}
          value={w.average === null ? null : Math.round(w.average)}
          missingReason={weeklyActiveText(w)}
          change={w.average === null ? null : countChange(w.average, pw, period)}
          selected={metric === "waa"}
          onSelect={() => setMetric("waa")}
        />
        <StatCard
          data-stat="days-missing"
          label="Days without data"
          definition={
            reasons.length
              ? `${DAYS_WITHOUT_DATA} ${reasons.map(([code, n]) => `${countOf(n, "day")}: ${missingText(code)}.`).join(" ")}`
              : DAYS_WITHOUT_DATA
          }
          value={a.daysMissing}
        />
      </StatGrid>
      {metric === "waa" ? (
        <WeeksChart view={view} previous={before.data ?? null} />
      ) : (
        <DaysChart view={view} previous={before.data ?? null} metric={metric} />
      )}
      {/* the weeks and the reasons of days without data as text, for a
          screen reader; the figures and the chart carry them on hover */}
      <div className="sr-only">
        <p data-testid="waa-summary">{weeklyActiveText(w)}</p>
        <WeeklyActiveList weeks={view.weeks} label="Weekly active accounts per ISO week" />
        <DailyReasons reasons={reasons} />
      </div>
    </section>
  );
}

function dailyReasons(daily: VerifiedActivity["daily"]): [string, number][] {
  const by = new Map<string, number>();
  for (const d of daily)
    if (d.value === null && d.missing && d.missing !== "day_not_complete")
      by.set(d.missing, (by.get(d.missing) ?? 0) + 1);
  return [...by.entries()].sort((x, y) => y[1] - x[1]);
}

function DailyReasons({ reasons }: { reasons: [string, number][] }) {
  if (reasons.length === 0) return null;
  return (
    <ul aria-label="Days without data">
      {reasons.map(([code, n]) => (
        <li key={code} data-missing-reason={code}>
          {countOf(n, "day")} without data. {missingText(code)}.
        </li>
      ))}
    </ul>
  );
}

// operations or transactions per day: one stacked part per verified id in
// the same colour, so the bar reads as the total and the readout splits
// the day by id
function DaysChart({
  view,
  previous,
  metric,
}: {
  view: OnchainView;
  previous: OnchainView | null;
  metric: "operations" | "transactions";
}) {
  const today = utcDay();
  const ids = view.ids.filter((id) => id.verification.status === "verified");
  const buckets: TimeBucket[] = view.days.map((d) => ({
    at: `${d}T00:00:00Z`,
    tick: formatTick(d, "day"),
    label: formatDate(d),
    partial: d === today,
  }));
  const series: TimeSeries[] = ids.map((id) => ({
    key: id.id,
    label: shortKey(id.strkey),
    tone: "series-4",
    values: id.series.map((p) => p[metric]),
  }));
  const prevIds = previous?.ids.filter((id) => id.verification.status === "verified") ?? [];
  const prev =
    previous && previous.days.length === view.days.length
      ? previous.days.map((_, i) => {
          let sum: number | null = null;
          for (const id of prevIds) {
            const v = id.series[i]?.[metric];
            if (v !== null && v !== undefined) sum = (sum ?? 0) + v;
          }
          return sum;
        })
      : null;
  const any = series.some((s) => s.values.some((v) => (v ?? 0) > 0));
  const label = metric === "operations" ? "Operations" : "Transactions";
  return (
    <TimeChart
      key={any ? "data" : "empty"}
      title={`${label} per day, UTC`}
      buckets={buckets}
      series={series}
      previous={prev}
      state={any ? "data" : "empty"}
      emptyTitle={`No ${label.toLowerCase()} in this range`}
    />
  );
}

// the weekly active accounts per iso week
function WeeksChart({ view, previous }: { view: OnchainView; previous: OnchainView | null }) {
  const today = utcDay();
  const buckets: TimeBucket[] = view.weeks.map((w) => ({
    at: `${w.from}T00:00:00Z`,
    tick: formatTick(w.from, "day"),
    label: formatWeekOf(w.from),
    partial: w.to >= today,
  }));
  const values = view.weeks.map((w) => w.active_accounts);
  const prev =
    previous && previous.weeks.length === view.weeks.length
      ? previous.weeks.map((w) => w.active_accounts)
      : null;
  const any = values.some((v) => (v ?? 0) > 0);
  return (
    <TimeChart
      key={any ? "data" : "empty"}
      title="Weekly active accounts per ISO week"
      buckets={buckets}
      series={[{ key: "waa", label: "Active accounts", tone: "series-4", values }]}
      previous={prev}
      state={any ? "data" : "empty"}
      emptyTitle="No counted week in this range"
    />
  );
}

// ---- accounts and contracts: one table row per id

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
  const [proving, setProving] = useState<ChainId | null>(null);
  const [details, setDetails] = useState<ChainId | null>(null);
  const refresh = useRefresh(tenantId);
  const range = { from: view.from, to: view.to };
  const today = utcDay();
  const contracts = view.ids.some((id) => id.kind === "contract");
  const columns: Column<ChainId>[] = [
    {
      key: "id",
      header: "ID",
      primary: true,
      cell: (id) => (
        <span className="flex min-w-0 items-center gap-3" data-id={id.strkey}>
          <KindIcon kind={id.kind} />
          <span className="flex min-w-0 flex-col">
            <span className="flex items-center gap-1">
              <StrKey strkey={id.strkey} />
              <CopyButton
                text={id.strkey}
                label={`Copy the ${id.kind} id`}
                iconOnly
                size="xs"
                variant="ghost"
              />
            </span>
            <KindLabel kind={id.kind} />
          </span>
        </span>
      ),
    },
    {
      key: "status",
      header: "Status",
      mobile: true,
      cell: (id) =>
        id.verification.status !== "verified" && id.verification.last_error ? (
          <Tooltip content={`Last check: ${verifyErrorText(id.verification.last_error)}`}>
            <button
              type="button"
              className="inline-flex rounded-full focus-ring"
              data-last-error={id.verification.last_error}
            >
              <VerifiedPill id={id} />
              <span className="sr-only">
                . Last check: {verifyErrorText(id.verification.last_error)}
              </span>
            </button>
          </Tooltip>
        ) : (
          <VerifiedPill id={id} />
        ),
    },
    {
      key: "daily",
      header: `Operations, ${countOf(view.days.length, "day")}`,
      minWidth: "min-w-48",
      cell: (id) =>
        id.verification.status === "verified" ? (
          <span
            role="img"
            aria-label={`Daily operations of ${id.strkey}`}
            className="block max-w-[222px]"
          >
            <MiniBars
              values={id.series.map((p) => p.operations ?? 0)}
              series={4}
              running={view.to === today}
              labels={id.series.map(
                (p) =>
                  `${formatDate(p.day)}: ${p.operations === null ? missingText(p.missing ?? "") : formatCount(p.operations)}`,
              )}
              className="h-[30px]"
            />
          </span>
        ) : canChange ? (
          <Button variant="outline" size="sm" onClick={() => setProving(id)}>
            Prove control
          </Button>
        ) : null,
    },
    {
      key: "total",
      header: "Total",
      align: "right",
      numeric: true,
      mobile: true,
      cell: (id) =>
        id.verification.status !== "verified" ? null : id.totals.days_read === 0 ? (
          <Missing reason="No ledger read in this range" />
        ) : (
          <span className="font-semibold text-fg">{formatCount(id.totals.operations)}</span>
        ),
    },
  ];
  if (contracts)
    columns.push({
      key: "functions",
      header: "Calls per function",
      minWidth: "min-w-56",
      cell: (id) => <FunctionSplit id={id} />,
    });
  return (
    <>
      <DataTable<ChainId>
        label="Accounts and contracts"
        title={{
          text: "Accounts and contracts",
          count: `${formatCount(view.ids.length)} of ${MAX_IDS}`,
        }}
        columns={columns}
        rows={view.ids}
        getRowKey={(id) => id.id}
        rowLabel={(id) => id.strkey}
        rowProps={(id) => ({ "data-row": id.strkey })}
        rowActions={(id) => [
          {
            label: id.kind === "contract" && id.functions ? "Details and calls" : "Details",
            icon: <FileText />,
            onSelect: () => setDetails(id),
          },
          canChange
            ? {
                label: id.verification.status === "verified" ? "Prove again" : "Prove control",
                icon: <ShieldCheck />,
                onSelect: () => setProving(id),
              }
            : null,
          canChange && isOwner ? removeAction(id, tenantId, refresh) : null,
        ]}
      />
      <ProveDialog id={proving} tenantId={tenantId} onOpenChange={(o) => !o && setProving(null)} />
      <DetailsDialog
        id={details}
        tenantId={tenantId}
        range={range}
        onOpenChange={(o) => !o && setDetails(null)}
      />
    </>
  );
}

const SHADES = ["bg-series-4", "bg-series-4/60", "bg-series-4/35"];

// a contract's calls per function as one split bar, the top two named; the
// full list, its daily calls and the csv are in the row's details
function FunctionSplit({ id }: { id: ChainId }) {
  if (id.kind !== "contract" || id.functions === null) return null;
  const by = new Map<string, number>();
  for (const f of id.functions.functions)
    by.set(functionLabel(f), (by.get(functionLabel(f)) ?? 0) + f.successful);
  const rows = [...by.entries()].sort((x, y) => y[1] - x[1]);
  const total = rows.reduce((s, [, n]) => s + n, 0);
  if (total === 0) return <span className="text-small text-fg-3">No calls</span>;
  const share = (n: number) => Math.round((n / total) * 100);
  return (
    <span className="flex min-w-0 flex-col gap-1.5" data-functions={id.strkey}>
      <span
        role="img"
        aria-label={`Calls per function: ${rows.map(([name, n]) => `${name} ${share(n)}%`).join(", ")}`}
        className="flex h-2 w-full max-w-[240px] gap-px overflow-hidden rounded-[4px] bg-line"
      >
        {rows.slice(0, 3).map(([name, n], i) => (
          <i
            key={name}
            className={`block h-full ${SHADES[i]}`}
            // the width is data; react writes it through the cssom
            style={{ width: `${(n / total) * 100}%` }}
          />
        ))}
      </span>
      <span aria-hidden className="flex flex-wrap items-center gap-x-3 font-mono text-caption">
        {rows.slice(0, 2).map(([name, n]) => (
          <span key={name} className="flex items-baseline gap-1.5">
            <span className="text-fg-2">{name}</span>
            <span className="text-fg-3 tabular">{share(n)}%</span>
          </span>
        ))}
        {rows.length > 2 ? <span className="text-fg-3">+{rows.length - 2}</span> : null}
      </span>
    </span>
  );
}

// everything about one id that the row does not show: its proof, its daily
// operations with the missing days, its home domain and a contract's
// calls per function with their daily view and csv
function DetailsDialog({
  id,
  tenantId,
  range,
  onOpenChange,
}: {
  id: ChainId | null;
  tenantId: string;
  range: { from: string; to: string };
  onOpenChange: (o: boolean) => void;
}) {
  const v = id?.verification;
  return (
    <Dialog
      open={id !== null}
      onOpenChange={onOpenChange}
      title={id ? `${id.kind === "account" ? "Account" : "Contract"} ${shortKey(id.strkey)}` : ""}
      size="md"
    >
      {id && v ? (
        <div className="flex min-w-0 flex-col gap-4">
          <span className="flex flex-wrap items-center gap-2">
            <VerifiedPill id={id} />
            {id.added_by === "enrollment" ? <Pill tone="neutral">From enrollment</Pill> : null}
          </span>
          {v.status === "verified" ? (
            <p className="text-small text-fg-2">
              Proven by {v.method ? METHOD_TEXT[v.method].toLowerCase() : "a proof"}
              {v.via ? ` through ${shortKey(v.via)}` : ""}
              {v.verified_at ? ` on ${formatDateTimeUtc(v.verified_at)}` : ""}.
            </p>
          ) : null}
          {v.status === "verified" && id.totals.days_read > 0 ? (
            <div className="flex min-w-0 flex-col gap-2">
              <IdTotals id={id} />
              {id.read.home_domain ? (
                <span className="text-caption text-fg-2">Home domain {id.read.home_domain}</span>
              ) : null}
              <DayStrip
                label={`Daily operations of ${id.strkey}, by day`}
                points={id.series.map((p) => ({
                  day: p.day,
                  value: p.operations,
                  missing: p.missing,
                }))}
              />
              <MissingDays id={id} />
            </div>
          ) : null}
          {v.status === "verified" ? (
            <ContractFunctionsBlock id={id} source={{ tenantId, range }} />
          ) : null}
        </div>
      ) : null}
    </Dialog>
  );
}

// ---- the two-way stellar.toml link: one row per id and domain, the link
// as a sequence of steps with the next one named and one action for it

const LINK_SITES_SHOWN = 5;

type LinkRow = {
  key: string;
  site: LinkSite;
  strkey: string;
  kind: "account" | "contract";
  steps: { key: string; label: string; done: boolean }[];
  twoWay: boolean;
};

export function linkRows(sites: LinkSite[]): LinkRow[] {
  const out: LinkRow[] = [];
  for (const s of sites) {
    const domain = { key: "domain", label: "Verify the domain", done: s.verified };
    const toml = {
      key: "toml",
      label:
        s.stellar_toml === "found" ? "Name the domain in stellar.toml" : "Publish stellar.toml",
      done: s.stellar_toml === "found" && s.org_url_matches,
    };
    for (const a of s.accounts)
      out.push({
        key: `${s.site_id}|${a.strkey}`,
        site: s,
        strkey: a.strkey,
        kind: "account",
        twoWay: a.two_way,
        steps: [
          domain,
          toml,
          { key: "listed", label: "List the account in stellar.toml", done: a.listed_in_toml },
          { key: "home", label: "Set the home domain", done: a.home_domain_matches },
        ],
      });
    for (const c of s.contracts)
      out.push({
        key: `${s.site_id}|${c.strkey}`,
        site: s,
        strkey: c.strkey,
        kind: "contract",
        twoWay: false,
        steps: [
          domain,
          toml,
          { key: "listed", label: "List the contract in stellar.toml", done: c.listed_in_toml },
        ],
      });
  }
  return out;
}

function LinkSection({ view, tenantId }: { view: OnchainView; tenantId: string }) {
  const [showAll, setShowAll] = useState(false);
  const hasVerifiedAccount = view.ids.some(
    (id) => id.kind === "account" && id.verification.status === "verified",
  );
  // every step of the link needs a verified account; without one the
  // section waits until the first step is done
  if (!hasVerifiedAccount) return null;
  const sites = showAll ? view.link.sites : view.link.sites.slice(0, LINK_SITES_SHOWN);
  const rows = linkRows(sites);
  const accounts = new Set(view.link.sites.flatMap((s) => s.accounts.map((a) => a.strkey)));
  const linked = new Set(
    view.link.sites.flatMap((s) => s.accounts.filter((a) => a.two_way).map((a) => a.strkey)),
  );
  const columns: Column<LinkRow>[] = [
    {
      key: "id",
      header: "ID and domain",
      primary: true,
      cell: (r) => (
        <span className="flex min-w-0 flex-col gap-0.5">
          <StrKey strkey={r.strkey} />
          <span className="truncate font-mono text-caption text-fg-2" title={r.site.domain}>
            {r.site.domain}
          </span>
        </span>
      ),
    },
    { key: "link", header: "Link", mobile: true, cell: (r) => <LinkProgress row={r} /> },
    {
      key: "fix",
      header: "Fix",
      hideHeader: true,
      align: "right",
      action: true,
      cell: (r) => <LinkFix row={r} tenantId={tenantId} />,
    },
  ];
  return (
    <Panel
      aria-label="stellar.toml link"
      data-link={view.link.two_way ? "two-way" : "open"}
      title={
        <span className="flex flex-wrap items-baseline gap-2">
          stellar.toml link
          <span className="text-small font-normal text-fg-3 tabular" data-testid="link-count">
            {formatCount(linked.size)} of {countOf(accounts.size, "account")} linked both ways
          </span>
        </span>
      }
      flush
    >
      {view.link.sites.length === 0 ? (
        // the link starts from a site domain: one line and the way there
        <p
          className="flex flex-wrap items-baseline gap-x-3 px-4 pb-4 text-small text-fg-2 sm:px-5"
          data-link="no-site"
        >
          No site to link
          <Link
            to={`/projects/${tenantId}/sites`}
            className="rounded-sm font-medium text-fg hover:underline focus-ring"
          >
            Add a site
          </Link>
        </p>
      ) : (
        <>
          <DataTable<LinkRow>
            label="stellar.toml link"
            columns={columns}
            rows={rows}
            getRowKey={(r) => r.key}
            rowLabel={(r) => `${r.strkey} on ${r.site.domain}`}
            rowProps={(r) => ({ "data-link-row": r.key })}
            pageSize={200}
          />
          {view.link.sites.length > LINK_SITES_SHOWN ? (
            <div className="px-4 py-3 sm:px-5">
              <Button
                variant="ghost"
                size="sm"
                aria-expanded={showAll}
                onClick={() => setShowAll((x) => !x)}
              >
                {showAll ? "Show fewer" : `Show all ${formatCount(view.link.sites.length)} sites`}
              </Button>
            </div>
          ) : null}
        </>
      )}
    </Panel>
  );
}

function LinkProgress({ row }: { row: LinkRow }) {
  const next = row.steps.findIndex((s) => !s.done);
  const done = row.steps.filter((s) => s.done).length;
  const states: SetupStepState[] = row.steps.map((s, i) =>
    s.done ? "done" : i === next ? "current" : "todo",
  );
  const step = next < 0 ? null : row.steps[next];
  return (
    <span
      className="flex min-w-0 max-w-[260px] flex-col gap-2"
      data-link-state={step ? step.key : "done"}
    >
      {step ? (
        <span className="flex flex-wrap items-baseline gap-2">
          <span className="text-small font-medium text-fg">{step.label}</span>
          <span className="text-caption text-fg-3 tabular">
            {done} of {row.steps.length}
          </span>
        </span>
      ) : (
        <span className="text-small text-fg">
          {row.kind === "account" ? (row.twoWay ? "Linked both ways" : "Linked") : "Listed"}
        </span>
      )}
      <SetupBar
        steps={row.steps.map((s, i) => ({
          key: s.key,
          label: s.label,
          state: states[i] ?? "todo",
        }))}
      />
    </span>
  );
}

// the one action for the next step of a link row
function LinkFix({ row, tenantId }: { row: LinkRow; tenantId: string }) {
  const [help, setHelp] = useState(false);
  const next = row.steps.find((s) => !s.done);
  if (!next) return null;
  const s = row.site;
  const site = `/projects/${tenantId}/sites/${s.site_id}`;
  const org = `https://${s.domain}`;
  switch (next.key) {
    case "domain":
      return (
        <Button asChild variant="outline" size="sm">
          <Link to={site}>Verify the domain</Link>
        </Button>
      );
    case "toml":
      return s.stellar_toml === "found" ? (
        <CopyButton
          text={`[DOCUMENTATION]\nORG_URL = "${org}"`}
          label="Copy the lines"
          accessibleLabel={`Copy the lines that name ${s.domain}${s.org_url ? `. It names ${s.org_url} now` : ""}`}
        />
      ) : (
        <Button asChild variant="outline" size="sm">
          <Link to={site}>How to publish it</Link>
        </Button>
      );
    case "listed":
      return (
        <CopyButton
          text={row.kind === "account" ? `ACCOUNTS = ["${row.strkey}"]` : row.strkey}
          label="Copy the line"
          accessibleLabel={`Copy the line that lists ${row.strkey}`}
        />
      );
    default:
      return (
        <>
          <Button variant="outline" size="sm" onClick={() => setHelp(true)}>
            How to set it
          </Button>
          <Dialog open={help} onOpenChange={setHelp} title="Set the home domain" size="sm">
            <div className="flex flex-col gap-3">
              <p className="text-body text-fg-2">
                Send a set options operation from {shortKey(row.strkey)} with {s.domain} as its home
                domain.
              </p>
              <CopyBox value={s.domain} label="the domain" />
            </div>
          </Dialog>
        </>
      );
  }
}
