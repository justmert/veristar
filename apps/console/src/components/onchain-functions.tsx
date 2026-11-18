import { useQuery } from "@tanstack/react-query";
import { ChevronDown, Download } from "lucide-react";
import { useState } from "react";
import {
  type ChainId,
  type ContractFunctions,
  exportPaths,
  type FunctionsSource,
  type FunctionsSummary,
  type FunctionUsage,
  functionsQuery,
} from "@/api/onchain";
import { type Column, DataTable } from "@/components/data-table";
import { DayStrip } from "@/components/onchain";
import { ErrorState, LoadingState, Missing, Skeleton } from "@/components/states";
import { Button } from "@/components/ui/button";
import { countOf, formatCount } from "@/lib/format";
import { missingText } from "@/lib/onchain";

// how a verified soroban contract is used: its calls per function and
// caller over the range, and per utc day on request. a day whose calls
// were not read is drawn as missing with its reason, never as zero.

// rows shown before "show all"; a contract day stores at most 64 functions
const FIRST_ROWS = 10;

const CALLER_TEXT: Record<FunctionUsage["caller"], string> = {
  account: "Accounts",
  contract: "Contracts",
};

const FAILED_NOTE =
  "Not counted for accounts: a failed transaction keeps no contract events, so the ledger API does not list it.";

export function functionLabel(f: Pick<FunctionUsage, "function">): string {
  return f.function === "{other}" ? "Other functions" : f.function;
}

// the read state of a range in one line
export function functionsDaysText(s: FunctionsSummary): string {
  const parts = [`${countOf(s.days_read, "day")} read`];
  if (s.days_no_call_tree)
    parts.push(`${formatCount(s.days_no_call_tree)} with calls of accounts only`);
  if (s.days_missing) parts.push(`${formatCount(s.days_missing)} without data`);
  return parts.join(", ");
}

type Row = Omit<FunctionUsage, "series">;

const COLUMNS: Column<Row>[] = [
  {
    key: "function",
    header: "Function",
    primary: true,
    minWidth: "min-w-40",
    truncate: (f) => functionLabel(f),
    cell: (f) => <code className="font-mono text-fg">{functionLabel(f)}</code>,
  },
  {
    key: "caller",
    header: "Called by",
    mobile: true,
    cell: (f) => CALLER_TEXT[f.caller],
  },
  {
    key: "successful",
    header: "Successful",
    align: "right",
    numeric: true,
    mobile: true,
    cell: (f) => formatCount(f.successful),
  },
  {
    key: "failed",
    header: "Failed",
    align: "right",
    numeric: true,
    cell: (f) => (f.failed === null ? <Missing reason={FAILED_NOTE} /> : formatCount(f.failed)),
  },
  {
    key: "peak",
    header: "Peak accounts a day",
    definition: "The most distinct accounts behind the calls on one UTC day.",
    align: "right",
    numeric: true,
    cell: (f) => formatCount(f.peak_daily_accounts),
  },
  {
    key: "days",
    header: "Days with calls",
    align: "right",
    numeric: true,
    mobile: true,
    cell: (f) => formatCount(f.days_with_calls),
  },
];

export function FunctionsTable({ summary, label }: { summary: FunctionsSummary; label: string }) {
  const [all, setAll] = useState(false);
  if (summary.functions.length === 0) {
    return (
      <p className="text-small text-fg-2">
        No calls on the days read. Calls show when the contract emits an event in a transaction.
      </p>
    );
  }
  const rows = all ? summary.functions : summary.functions.slice(0, FIRST_ROWS);
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <DataTable<Row>
        label={label}
        columns={COLUMNS}
        rows={rows}
        getRowKey={(f) => `${f.caller}/${f.function}`}
        rowLabel={(f) => `${functionLabel(f)}, ${CALLER_TEXT[f.caller].toLowerCase()}`}
        rowProps={(f) => ({ "data-function": f.function, "data-caller": f.caller })}
      />
      {summary.functions.length > FIRST_ROWS ? (
        <Button
          variant="ghost"
          size="sm"
          className="w-fit print:hidden"
          onClick={() => setAll(!all)}
        >
          {all ? "Show fewer" : `Show all ${summary.functions.length}`}
        </Button>
      ) : null}
    </div>
  );
}

// the daily calls of each function: successful calls per day, and the
// reasons for the days without data
export function FunctionsDaily({ data }: { data: ContractFunctions }) {
  const reasons = new Map<string, number>();
  for (const d of data.days) {
    if (d.missing && d.missing !== "day_not_complete")
      reasons.set(d.missing, (reasons.get(d.missing) ?? 0) + 1);
  }
  const partial = data.days.filter((d) => d.state === "no_call_tree").length;
  return (
    <div className="flex flex-col gap-4">
      {reasons.size || partial ? (
        <ul className="flex flex-col gap-1 text-caption text-fg-2">
          {[...reasons.entries()].map(([code, n]) => (
            <li key={code}>
              {countOf(n, "day")} without data. {missingText(code)}
            </li>
          ))}
          {partial ? (
            <li>
              {countOf(partial, "day")} with the calls of accounts only:{" "}
              {missingText("no_call_tree")}
            </li>
          ) : null}
        </ul>
      ) : null}
      {data.functions.map((f) => (
        <div key={`${f.caller}/${f.function}`} className="flex flex-col gap-1">
          <span className="text-caption text-fg-2">
            <code className="font-mono text-fg">{functionLabel(f)}</code> called by{" "}
            {CALLER_TEXT[f.caller].toLowerCase()}, successful calls per day
          </span>
          <DayStrip
            label={`Daily calls of ${functionLabel(f)} by ${CALLER_TEXT[f.caller].toLowerCase()}`}
            points={(f.series ?? []).map((p, i) => ({
              day: p.day,
              value: p.successful,
              missing:
                p.successful === null
                  ? (data.days[i]?.missing ??
                    (data.days[i]?.state === "no_call_tree" ? "no_call_tree" : null))
                  : null,
            }))}
          />
        </div>
      ))}
    </div>
  );
}

function DailyCalls({ id, source }: { id: ChainId; source: FunctionsSource }) {
  const q = useQuery(functionsQuery(source, id.id));
  if (q.isPending)
    return (
      <LoadingState label="Loading the daily calls">
        <Skeleton.Table rows={2} columns={2} />
      </LoadingState>
    );
  if (q.error)
    return (
      <ErrorState
        compact
        title="The daily calls did not load"
        error={q.error}
        onRetry={() => q.refetch()}
      />
    );
  return <FunctionsDaily data={q.data} />;
}

// the calls per function of one declared contract, as a part of the
// contract's row (a divider above it, no box of its own). an unverified
// contract has none and draws nothing. source, when given, lets the reader
// open the daily calls and export them.
export function ContractFunctionsBlock({ id, source }: { id: ChainId; source?: FunctionsSource }) {
  const [open, setOpen] = useState(false);
  if (id.kind !== "contract") return null;
  const s = id.functions;
  if (s === null) return null;
  return (
    <section
      aria-label={`Calls per function of ${id.strkey}`}
      className="flex min-w-0 flex-col gap-3 border-t border-line-subtle pt-3"
    >
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <h4 className="text-body font-medium text-fg">Calls per function</h4>
        <span className="text-caption text-fg-2" data-testid="functions-days">
          {functionsDaysText(s)}
        </span>
      </div>
      <FunctionsTable summary={s} label={`Calls per function of ${id.strkey}`} />
      {source ? (
        <div className="flex flex-wrap items-center gap-2">
          <Button
            variant="ghost"
            size="sm"
            aria-expanded={open}
            onClick={() => setOpen(!open)}
            className="max-lg:h-(--vs-control-touch)"
          >
            <ChevronDown
              aria-hidden
              className={
                open
                  ? "rotate-180 transition-transform duration-(--vs-duration-press) ease-standard"
                  : "transition-transform duration-(--vs-duration-press) ease-standard"
              }
            />
            Daily calls
          </Button>
          <Button asChild variant="ghost" size="sm" className="max-lg:h-(--vs-control-touch)">
            <a href={exportPaths.functions(source, id.id)} download>
              <Download aria-hidden />
              Export CSV
            </a>
          </Button>
        </div>
      ) : null}
      {source && open ? (
        <div className="animate-rise-in">
          <DailyCalls id={id} source={source} />
        </div>
      ) : null}
    </section>
  );
}
