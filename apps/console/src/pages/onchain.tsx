import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Blocks, Plus, Trash2 } from "lucide-react";
import { type FormEvent, useState } from "react";
import { ApiError } from "@/api/client";
import {
  type ChainChallenge,
  type ChainId,
  type LinkSite,
  type OnchainView,
  onchainApi,
  onchainKeys,
  onchainQuery,
} from "@/api/onchain";
import { CopyField } from "@/components/code-block";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { ErrorPanel, FormMessage, Loading, StateBlock } from "@/components/error-panel";
import { TextField } from "@/components/form-field";
import { projectTabs } from "@/components/nav/nav-items";
import { NavTabs } from "@/components/nav/nav-tabs";
import { DayStrip, KindLabel, LinkStatus, VerifiedPill } from "@/components/onchain";
import { ContractFunctionsBlock } from "@/components/onchain-functions";
import { PageHeader, Panel } from "@/components/panel";
import { Pill } from "@/components/status";
import { Button } from "@/components/ui/button";
import { WalletPicker } from "@/components/wallet-picker";
import { formatCount, formatDateTimeUtc } from "@/lib/format";
import { METHOD_TEXT, missingReasons, missingText, verifyErrorText } from "@/lib/onchain";
import { connectWallet, signMessage, WalletError } from "@/lib/stellar-wallet";
import { useProject } from "./project-layout";

const RANGE = {};

// add, prove and remove the project's accounts and contracts, and see the
// two-way stellar.toml link (contract 15). every number on the page comes
// from the daily rows the chain-activity job stored.
export function OnchainPage() {
  const { tenant, termsCurrent, isOwner } = useProject();
  const canChange = termsCurrent && tenant.status !== "suspended";
  const view = useQuery(onchainQuery(tenant.id, RANGE));

  if (view.isPending) return <Loading label="Loading on-chain activity" />;
  if (view.error) return <ErrorPanel error={view.error} onRetry={() => view.refetch()} />;
  const v = view.data;

  return (
    <div className="flex flex-col gap-5">
      <NavTabs label="On-chain" items={projectTabs(tenant.id, "onchain") ?? []} />
      <PageHeader
        title="Accounts and contracts"
        description="The Stellar accounts and Soroban contracts your project operates. Prove control of each, and its ledger activity shows next to your web and API usage, for you and for SCF reviewers."
      />
      {canChange ? <AddForm tenantId={tenant.id} /> : null}
      {v.ids.length === 0 ? (
        <StateBlock
          kind="empty"
          icon={<Blocks aria-hidden strokeWidth={1.75} />}
          title="Nothing declared yet"
        >
          Add an account (G…) or a contract (C…) above. Ids you gave at enrollment appear here on
          their own.
        </StateBlock>
      ) : (
        <Panel
          title="Declared ids"
          description={`Activity over ${v.from} to ${v.to}, per UTC day.`}
          flush
        >
          <ul className="flex flex-col divide-y divide-line">
            {v.ids.map((id) => (
              <IdItem
                key={id.id}
                id={id}
                tenantId={tenant.id}
                canChange={canChange}
                isOwner={isOwner}
              />
            ))}
          </ul>
        </Panel>
      )}
      <LinkPanel view={v} />
      <p className="text-caption text-fg-3">
        Ledger data is public. Veristar stores the ids you declare, their proofs and daily counts of
        operations, transactions and counterparties, and for contracts the daily calls per function
        with the number of distinct accounts behind them, never the counterparties or callers
        themselves. Accounts are read from Horizon; contracts from the events they emit through
        Stellar RPC, which keeps about seven days, so a contract's history starts the day you add
        it.
      </p>
    </div>
  );
}

// refetches the project's on-chain view after a change
function useRefresh(tenantId: string) {
  const qc = useQueryClient();
  return () => qc.invalidateQueries({ queryKey: onchainKeys.all(tenantId) });
}

function AddForm({ tenantId }: { tenantId: string }) {
  const refresh = useRefresh(tenantId);
  const [strkey, setStrkey] = useState("");
  const [error, setError] = useState<string | null>(null);
  const add = useMutation({
    mutationFn: (s: string) => onchainApi.add(tenantId, s),
    onSuccess: async () => {
      setStrkey("");
      await refresh();
    },
    onError: (err) => {
      if (err instanceof ApiError && err.fields.strkey) {
        setError(
          err.fields.strkey === "duplicate"
            ? "The project already declares this id."
            : err.fields.strkey === "too_many"
              ? "A project declares at most 20 accounts and contracts."
              : "Enter a Stellar account (G…) or a Soroban contract (C…). A muxed account (M…) is added as its G account.",
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
      className="flex flex-col gap-2 rounded-lg border border-line bg-raised p-4 sm:flex-row sm:items-end"
    >
      <div className="min-w-0 flex-1">
        <TextField
          label="Account or contract"
          name="strkey"
          value={strkey}
          onChange={(e) => setStrkey(e.target.value)}
          placeholder="G… or C…"
          autoComplete="off"
          spellCheck={false}
          error={error ?? undefined}
        />
      </div>
      <Button type="submit" disabled={add.isPending}>
        <Plus aria-hidden />
        {add.isPending ? "Adding" : "Add"}
      </Button>
      {add.error && !(add.error instanceof ApiError && add.error.fields.strkey) ? (
        <FormMessage error={add.error} />
      ) : null}
    </form>
  );
}

function IdItem({
  id,
  tenantId,
  canChange,
  isOwner,
}: {
  id: ChainId;
  tenantId: string;
  canChange: boolean;
  isOwner: boolean;
}) {
  const refresh = useRefresh(tenantId);
  const [confirm, setConfirm] = useState(false);
  const remove = useMutation({
    mutationFn: () => onchainApi.remove(tenantId, id.id),
    onSuccess: async () => {
      setConfirm(false);
      await refresh();
    },
  });
  const v = id.verification;
  const reasons = missingReasons(id);
  return (
    <li className="flex flex-col gap-3 px-4 py-4 sm:px-5">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <KindLabel kind={id.kind} />
        <code className="min-w-0 break-all text-small text-fg">{id.strkey}</code>
        <VerifiedPill id={id} />
        {id.added_by === "enrollment" ? (
          <span className="text-caption text-fg-3">from enrollment</span>
        ) : null}
        {canChange && isOwner ? (
          <Button
            variant="ghost"
            size="xs"
            className="ml-auto"
            onClick={() => setConfirm(true)}
            aria-label={`Remove ${id.strkey}`}
          >
            <Trash2 aria-hidden />
            Remove
          </Button>
        ) : null}
      </div>
      {v.status === "verified" ? (
        <p className="text-small text-fg-2">
          Proven by {v.method ? METHOD_TEXT[v.method].toLowerCase() : "a proof"}
          {v.via ? ` through ${v.via}` : ""}
          {v.verified_at ? ` on ${formatDateTimeUtc(v.verified_at)}` : ""}.
        </p>
      ) : null}
      {v.last_error ? (
        <p className="text-small text-warning">Last check: {verifyErrorText(v.last_error)}</p>
      ) : null}
      {canChange ? (
        id.kind === "account" ? (
          <AccountProof id={id} tenantId={tenantId} />
        ) : (
          <ContractProof id={id} tenantId={tenantId} />
        )
      ) : null}
      <div className="flex flex-col gap-1.5">
        <span className="text-caption text-fg-3">
          {formatCount(id.totals.operations)} operations · {formatCount(id.totals.transactions)}{" "}
          transactions · peak {formatCount(id.totals.peak_counterparties)} counterparties a day
          {id.read.home_domain ? ` · home domain ${id.read.home_domain}` : ""}
        </span>
        <DayStrip
          label={`Daily operations of ${id.strkey}`}
          points={id.series.map((p) => ({ day: p.day, value: p.operations, missing: p.missing }))}
        />
        {reasons.map((r) => (
          <span key={r.code} className="text-caption text-fg-2">
            {r.days} {r.days === 1 ? "day" : "days"} without data: {missingText(r.code)}
          </span>
        ))}
      </div>
      <ContractFunctionsBlock id={id} source={{ tenantId, range: RANGE }} />
      <ConfirmDialog
        open={confirm}
        onOpenChange={setConfirm}
        title={`Remove ${id.kind === "account" ? "account" : "contract"}`}
        description="The id leaves the project's footprint and its activity is no longer shown. Adding it again brings its history back."
        confirmLabel="Remove"
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
  const create = useMutation({
    mutationFn: () => onchainApi.challenge(tenantId, id.id),
    onSuccess: (c) => setChallenge(c),
  });
  const check = useMutation({
    mutationFn: (body: { method: "sep53" | "manage_data"; signature?: string }) =>
      onchainApi.verify(tenantId, id.id, body),
    onSuccess: async (r) => {
      if (r.verification.status === "verified" && !r.verification.last_error) setChallenge(null);
      await refresh();
    },
  });
  if (!challenge) {
    return (
      <div className="flex flex-wrap items-center gap-2">
        <Button
          variant="outline"
          size="sm"
          onClick={() => create.mutate()}
          disabled={create.isPending}
        >
          {id.verification.status === "verified" ? "Prove again" : "Prove control"}
        </Button>
        {create.error ? <FormMessage error={create.error} /> : null}
      </div>
    );
  }
  return (
    <div className="flex flex-col gap-3 rounded-md border border-line p-3">
      <p className="text-small text-fg-2">
        Prove that the project controls {id.strkey} one of two ways before{" "}
        {formatDateTimeUtc(challenge.expires_at)}.
      </p>
      <div className="flex flex-col gap-2">
        <span className="text-small font-medium text-fg">Sign with a wallet</span>
        <p className="text-small text-fg-2">
          Your wallet signs the message below with SEP-53. Signing a message moves no funds and
          sends no transaction.
        </p>
        <pre className="overflow-x-auto rounded-md bg-sunken p-2 text-caption text-fg-2">
          {challenge.message}
        </pre>
        <Button size="sm" className="w-fit" onClick={() => setPicker(true)}>
          Sign with a wallet
        </Button>
      </div>
      <div className="flex flex-col gap-2">
        <span className="text-small font-medium text-fg">Or publish a data entry</span>
        <p className="text-small text-fg-2">
          Submit a manage-data operation from the account with this name and value (any wallet or
          the Stellar Lab can build it), then check. You can delete the entry afterwards.
        </p>
        <dl className="grid gap-2 sm:grid-cols-2">
          <div className="flex min-w-0 flex-col gap-1">
            <dt className="text-caption text-fg-3">Entry name</dt>
            <dd>
              <CopyField label="the entry name" value={challenge.manage_data.name} />
            </dd>
          </div>
          <div className="flex min-w-0 flex-col gap-1">
            <dt className="text-caption text-fg-3">Entry value</dt>
            <dd>
              <CopyField label="the entry value" value={challenge.manage_data.value} />
            </dd>
          </div>
        </dl>
        <Button
          size="sm"
          variant="outline"
          className="w-fit"
          disabled={check.isPending}
          onClick={() => check.mutate({ method: "manage_data" })}
        >
          {check.isPending ? "Checking" : "Check the data entry"}
        </Button>
      </div>
      {check.error ? <FormMessage error={check.error} /> : null}
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
              `The wallet's active account is ${address}. Switch it to ${id.strkey} and try again.`,
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

// a contract is proven through a verified account of the project: as the
// admin or owner its instance storage names, or as its deployer
function ContractProof({ id, tenantId }: { id: ChainId; tenantId: string }) {
  const refresh = useRefresh(tenantId);
  const [tx, setTx] = useState("");
  const check = useMutation({
    mutationFn: (body: { method: "contract_admin" | "contract_deployer"; tx_hash?: string }) =>
      onchainApi.verify(tenantId, id.id, body),
    onSuccess: async () => {
      await refresh();
    },
  });
  return (
    <div className="flex flex-col gap-3 rounded-md border border-line p-3">
      <p className="text-small text-fg-2">
        A contract is proven through an account the project has verified: either the contract stores
        it as its admin or owner, or it deployed the contract.
      </p>
      <div className="flex flex-wrap items-center gap-2">
        <Button
          size="sm"
          variant="outline"
          disabled={check.isPending}
          onClick={() => check.mutate({ method: "contract_admin" })}
        >
          Check the admin
        </Button>
      </div>
      <form
        className="flex flex-col gap-2 sm:flex-row sm:items-end"
        onSubmit={(e) => {
          e.preventDefault();
          check.mutate({ method: "contract_deployer", tx_hash: tx.trim().toLowerCase() });
        }}
      >
        <div className="min-w-0 flex-1">
          <TextField
            label="Deploying transaction"
            hint="The hash of the transaction that created the contract, within the last year."
            value={tx}
            onChange={(e) => setTx(e.target.value)}
            placeholder="64 hex characters"
            spellCheck={false}
            autoComplete="off"
          />
        </div>
        <Button
          type="submit"
          size="sm"
          variant="outline"
          disabled={check.isPending || tx.trim().length !== 64}
        >
          Check the deployer
        </Button>
      </form>
      {check.error ? <FormMessage error={check.error} /> : null}
    </div>
  );
}

function Leg({ ok, children }: { ok: boolean; children: React.ReactNode }) {
  return (
    <li className="flex items-center gap-2 text-small">
      <Pill tone={ok ? "success" : "info"}>{ok ? "Yes" : "No"}</Pill>
      <span className="text-fg-2">{children}</span>
    </li>
  );
}

function LinkSiteBlock({ s }: { s: LinkSite }) {
  return (
    <div className="flex flex-col gap-2 py-3 first:pt-0 last:pb-0">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-body font-medium text-fg">{s.domain}</span>
        {s.two_way ? (
          <Pill tone="success">Linked both ways</Pill>
        ) : (
          <Pill tone="info">Not linked</Pill>
        )}
      </div>
      <ul className="flex flex-col gap-1">
        <Leg ok={s.verified}>The domain is verified in Veristar</Leg>
        <Leg ok={s.stellar_toml === "found"}>
          A stellar.toml is published at https://{s.domain}/.well-known/stellar.toml
        </Leg>
        <Leg ok={s.org_url_matches}>
          Its [DOCUMENTATION] ORG_URL is on {s.domain}
          {s.org_url ? ` (now ${s.org_url})` : ""}
        </Leg>
      </ul>
      {s.accounts.length ? (
        <ul className="flex flex-col gap-1">
          {s.accounts.map((a) => (
            <li key={a.strkey} className="flex flex-wrap items-center gap-2 text-small text-fg-2">
              <code className="text-fg">{a.strkey.slice(0, 6)}…</code>
              <span>{a.listed_in_toml ? "listed in ACCOUNTS" : "not in ACCOUNTS"}</span>
              <span>·</span>
              <span>
                home domain{" "}
                {a.home_domain
                  ? `${a.home_domain}${a.home_domain_matches ? "" : " (another domain)"}`
                  : "not set"}
              </span>
              {a.two_way ? <Pill tone="success">Two-way</Pill> : null}
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}

function LinkPanel({ view }: { view: OnchainView }) {
  return (
    <Panel
      title="stellar.toml link"
      description="SEP-1 links a domain and its accounts both ways: the stellar.toml on your verified domain lists the account under ACCOUNTS and names the domain in ORG_URL, and the account's home_domain is that domain."
      actions={view.ids.length ? <LinkStatus view={view} /> : null}
    >
      {view.link.sites.length === 0 ? (
        <p className="text-body text-fg-2">
          Add and verify a site first; the link starts from its domain.
        </p>
      ) : (
        <div className="flex flex-col divide-y divide-line">
          {view.link.sites.map((s) => (
            <LinkSiteBlock key={s.site_id} s={s} />
          ))}
        </div>
      )}
    </Panel>
  );
}
