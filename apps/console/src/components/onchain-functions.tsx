import { useQuery } from "@tanstack/react-query";
import { ChevronDown, ChevronRight, Download } from "lucide-react";
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
import { ErrorPanel, Loading } from "@/components/error-panel";
import { DayStrip } from "@/components/onchain";
import { Button } from "@/components/ui/button";
import { formatCount } from "@/lib/format";
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
  "Calls of accounts come from successful transactions only: a failed transaction keeps no contract events, so the source does not list it";

export function functionLabel(f: Pick<FunctionUsage, "function">): string {
  return f.function === "{other}" ? "Other functions" : f.function;
}

// the read state of a range in one line
export function functionsDaysText(s: FunctionsSummary): string {
  const parts = [`${formatCount(s.days_read)} ${s.days_read === 1 ? "day" : "days"} read`];
  if (s.days_no_call_tree)
    parts.push(`${formatCount(s.days_no_call_tree)} with calls of accounts only`);
  if (s.days_missing) parts.push(`${formatCount(s.days_missing)} without data`);
  return parts.join(", ");
}

export function FunctionsTable({ summary, label }: { summary: FunctionsSummary; label: string }) {
  const [all, setAll] = useState(false);
  const rows = all ? summary.functions : summary.functions.slice(0, FIRST_ROWS);
  if (summary.functions.length === 0) {
    return (
      <p className="text-small text-fg-2">
        No calls found on the days read. Calls are found in the transactions where the contract
        emits an event.
      </p>
    );
  }
  return (
    <div className="flex flex-col gap-2">
      <div className="overflow-x-auto">
        <table aria-label={label} className="w-full min-w-[34rem] text-small">
          <thead>
            <tr className="text-left text-caption text-fg-3">
              <th className="py-1 pr-3 font-normal">Function</th>
              <th className="py-1 pr-3 font-normal">Called by</th>
              <th className="py-1 pr-3 text-right font-normal">Successful</th>
              <th className="py-1 pr-3 text-right font-normal">Failed</th>
              <th className="py-1 pr-3 text-right font-normal">Peak accounts a day</th>
              <th className="py-1 text-right font-normal">Days with calls</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line">
            {rows.map((f) => (
              <tr
                key={`${f.caller}/${f.function}`}
                data-function={f.function}
                data-caller={f.caller}
              >
                <td className="py-1.5 pr-3">
                  <code className="text-fg">{functionLabel(f)}</code>
                </td>
                <td className="py-1.5 pr-3 text-fg-2">{CALLER_TEXT[f.caller]}</td>
                <td className="py-1.5 pr-3 text-right tabular-nums">{formatCount(f.successful)}</td>
                <td
                  className="py-1.5 pr-3 text-right tabular-nums"
                  title={f.failed === null ? FAILED_NOTE : undefined}
                >
                  {f.failed === null ? "n/a" : formatCount(f.failed)}
                </td>
                <td className="py-1.5 pr-3 text-right tabular-nums">
                  {formatCount(f.peak_daily_accounts)}
                </td>
                <td className="py-1.5 text-right tabular-nums">{formatCount(f.days_with_calls)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {summary.functions.length > FIRST_ROWS ? (
        <Button variant="ghost" size="xs" className="w-fit" onClick={() => setAll(!all)}>
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
    <div className="flex flex-col gap-3">
      {[...reasons.entries()].map(([code, n]) => (
        <span key={code} className="text-caption text-fg-2">
          {n} {n === 1 ? "day" : "days"} without data: {missingText(code)}
        </span>
      ))}
      {partial ? (
        <span className="text-caption text-fg-2">
          {partial} {partial === 1 ? "day" : "days"} with the calls of accounts only:{" "}
          {missingText("no_call_tree")}
        </span>
      ) : null}
      {data.functions.map((f) => (
        <div key={`${f.caller}/${f.function}`} className="flex flex-col gap-1">
          <span className="text-caption text-fg-3">
            <code className="text-fg-2">{functionLabel(f)}</code> called by{" "}
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
  if (q.isPending) return <Loading label="Loading daily calls" />;
  if (q.error) return <ErrorPanel error={q.error} onRetry={() => q.refetch()} />;
  return <FunctionsDaily data={q.data} />;
}

// the calls per function of one declared contract. source, when given,
// lets the reader open the daily calls and export them.
export function ContractFunctionsBlock({ id, source }: { id: ChainId; source?: FunctionsSource }) {
  const [open, setOpen] = useState(false);
  if (id.kind !== "contract") return null;
  const s = id.functions;
  return (
    <section
      aria-label={`Calls per function of ${id.strkey}`}
      className="flex flex-col gap-2 rounded-md border border-line bg-sunken px-3 py-2.5"
    >
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <h4 className="text-body font-medium text-fg">Calls per function</h4>
        {s ? (
          <span className="text-caption text-fg-3" data-testid="functions-days">
            {functionsDaysText(s)}
          </span>
        ) : null}
      </div>
      {s === null ? (
        <p className="text-small text-fg-2">
          Calls per function show once the contract is verified. Only verified contracts count.
        </p>
      ) : (
        <>
          <FunctionsTable summary={s} label={`Calls per function of ${id.strkey}`} />
          {source ? (
            <div className="flex flex-wrap items-center gap-2">
              <Button variant="ghost" size="xs" aria-expanded={open} onClick={() => setOpen(!open)}>
                {open ? <ChevronDown aria-hidden /> : <ChevronRight aria-hidden />}
                Daily calls
              </Button>
              <Button asChild variant="outline" size="xs">
                <a href={exportPaths.functions(source, id.id)} download>
                  <Download aria-hidden />
                  Export CSV
                </a>
              </Button>
            </div>
          ) : null}
          {source && open ? <DailyCalls id={id} source={source} /> : null}
        </>
      )}
    </section>
  );
}
