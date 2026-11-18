import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Download, ExternalLink, FileSignature } from "lucide-react";
import { type FormEvent, useState } from "react";
import {
  fileJSON,
  type ReportSummary,
  reportApi,
  reportKeysOf,
  reportsQuery,
  type SignedFile,
} from "@/api/reports";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { CopyButton } from "@/components/copy-button";
import { ErrorPanel, FormMessage, Loading, StateBlock } from "@/components/error-panel";
import { TextField } from "@/components/form-field";
import { PageHeader, Panel } from "@/components/panel";
import { Pill } from "@/components/status";
import { Button } from "@/components/ui/button";
import { formatCount, formatDateTimeUtc } from "@/lib/format";
import { formatDayRange, isDay, lastDays } from "@/lib/global";
import { useProject } from "./project-layout";

// signed usage reports (contract 20): a member generates a report of closed
// utc days, signed with the platform's stellar key, and shares its link

const MAX_DAYS = 366;

// a report ends this many days before today at the latest: the daily
// aggregation and grading read today and the three days before it again,
// so only older days are final and a signed number never changes
export const FINAL_DAYS = 4;

// the last final day, as yyyy-mm-dd in utc
export function lastFinalDay(now: Date): string {
  return lastDays(1, new Date(now.getTime() - FINAL_DAYS * 86_400_000)).to;
}

// the last n final days
export function closedDays(n: number, now: Date): { from: string; to: string } {
  return lastDays(n, new Date(now.getTime() - FINAL_DAYS * 86_400_000));
}

// the bounds of 20.3, checked before sending; the server checks them again
export function validatePeriod(
  from: string,
  to: string,
  now: Date,
): { from?: string; to?: string } {
  const out: { from?: string; to?: string } = {};
  if (!isDay(from)) out.from = "Enter a start day.";
  if (!isDay(to)) out.to = "Enter an end day.";
  if (out.from || out.to) return out;
  const last = lastFinalDay(now);
  if (to > last)
    out.to = `End on ${last} or earlier (UTC): the last ${FINAL_DAYS} days can still change.`;
  else if (to < from) out.to = "End on or after the start.";
  else if ((Date.parse(to) - Date.parse(from)) / 86_400_000 + 1 > MAX_DAYS)
    out.to = `A report covers at most ${MAX_DAYS} days.`;
  return out;
}

// saves the signed file as the json people attach to a submission
export function downloadFile(file: SignedFile, name: string) {
  const blob = new Blob([fileJSON(file)], { type: "application/json" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  document.body.append(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}

export function ReportsPage() {
  const { tenant } = useProject();
  const list = useQuery(reportsQuery(tenant.id));
  const queryClient = useQueryClient();
  const initial = closedDays(30, new Date());
  const [from, setFrom] = useState(initial.from);
  const [to, setTo] = useState(initial.to);
  const [errors, setErrors] = useState<{ from?: string; to?: string }>({});
  const [revoking, setRevoking] = useState<ReportSummary | null>(null);
  const create = useMutation({
    mutationFn: () => reportApi.create(tenant.id, { from, to }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: reportKeysOf.list(tenant.id) }),
  });
  const revoke = useMutation({
    mutationFn: (id: string) => reportApi.revoke(tenant.id, id),
    onSuccess: async () => {
      setRevoking(null);
      await queryClient.invalidateQueries({ queryKey: reportKeysOf.list(tenant.id) });
    },
  });
  const download = useMutation({
    mutationFn: async (r: ReportSummary) => {
      const d = await reportApi.get(tenant.id, r.id);
      downloadFile(d.file, `veristar-report-${tenant.slug}-${r.from}-${r.to}.json`);
    },
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    const found = validatePeriod(from, to, new Date());
    setErrors(found);
    if (!found.from && !found.to) create.mutate();
  }

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Signed reports"
        description="A usage report of your project for a period of closed UTC days: web, API and on-chain numbers from the same aggregates SCF operators read, your integrity grade with its reasons, and your cohort and percentiles. Veristar signs it with its Stellar key, so anyone can check it without an account."
      />
      <Panel title="Generate a report" aria-label="Generate a report">
        <form onSubmit={submit} noValidate className="flex flex-col gap-3">
          <div className="grid gap-3 sm:grid-cols-[repeat(2,minmax(0,12rem))_auto] sm:items-start">
            <TextField
              label="From"
              type="date"
              value={from}
              onChange={(e) => setFrom(e.target.value)}
              error={errors.from}
            />
            <TextField
              label="To"
              type="date"
              value={to}
              onChange={(e) => setTo(e.target.value)}
              error={errors.to}
              hint={`Up to ${lastFinalDay(new Date())} (UTC), at most ${MAX_DAYS} days.`}
            />
          </div>
          <FormMessage error={create.error} />
          {create.data ? (
            <p role="status" className="text-small text-fg">
              Report generated. Its link:{" "}
              <a
                href={create.data.url}
                className="break-all rounded-sm text-accent-text underline-offset-4 hover:underline focus-ring"
              >
                {create.data.url}
              </a>
            </p>
          ) : null}
          <div>
            <Button type="submit" pending={create.isPending}>
              <FileSignature aria-hidden />
              {create.isPending ? "Signing" : "Generate and sign"}
            </Button>
          </div>
        </form>
      </Panel>

      <Panel title="Your reports" aria-label="Your reports" flush>
        {list.isPending ? <Loading label="Loading reports" className="px-4" /> : null}
        {list.error ? (
          <ErrorPanel error={list.error} onRetry={() => list.refetch()} className="m-4" />
        ) : null}
        {list.data ? (
          list.data.items.length === 0 ? (
            <StateBlock
              kind="empty"
              icon={<FileSignature aria-hidden />}
              title="No report yet"
              className="m-4"
            >
              Generate one above, then share its link in your SCF submission.
            </StateBlock>
          ) : (
            <ul className="flex flex-col divide-y divide-line" aria-label="Reports">
              {list.data.items.map((r) => {
                // the buttons repeat on every row; their names say which report
                const of = `of the report for ${formatDayRange(r.from, r.to)}`;
                return (
                  <li
                    key={r.id}
                    data-report={r.id}
                    className="flex flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3"
                  >
                    <span className="flex min-w-0 flex-col gap-1">
                      <span className="text-body font-medium text-fg">
                        {formatDayRange(r.from, r.to)}
                      </span>
                      <span className="text-caption text-fg-3">
                        Generated {formatDateTimeUtc(r.created_at)}, {formatCount(r.view_count)}{" "}
                        {r.view_count === 1 ? "view" : "views"}
                      </span>
                    </span>
                    {r.revoked_at ? <Pill tone="warning">Link revoked</Pill> : null}
                    <span className="ml-auto flex flex-wrap gap-1">
                      {r.revoked_at ? null : (
                        <>
                          <Button asChild size="sm" variant="outline">
                            <a
                              href={r.url}
                              target="_blank"
                              rel="noopener"
                              aria-label={`Open the link ${of}`}
                            >
                              <ExternalLink aria-hidden />
                              Open
                            </a>
                          </Button>
                          <CopyButton
                            text={r.url}
                            label="Copy link"
                            accessibleLabel={`Copy link ${of}`}
                          />
                        </>
                      )}
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => download.mutate(r)}
                        pending={download.isPending && download.variables?.id === r.id}
                        aria-label={`JSON ${of}`}
                      >
                        <Download aria-hidden />
                        JSON
                      </Button>
                      {r.revoked_at ? null : (
                        <Button
                          size="sm"
                          variant="ghost"
                          onClick={() => setRevoking(r)}
                          aria-label={`Revoke link ${of}`}
                        >
                          Revoke link
                        </Button>
                      )}
                    </span>
                  </li>
                );
              })}
            </ul>
          )
        ) : null}
        <FormMessage error={download.error} className="m-4" />
      </Panel>

      <ConfirmDialog
        open={revoking !== null}
        onOpenChange={(o) => {
          if (!o) {
            setRevoking(null);
            // a refusal shown for one report must not greet the next
            revoke.reset();
          }
        }}
        title="Revoke this link?"
        description="Anyone who opens the link sees that the report is not available. A JSON file already downloaded keeps its valid signature. A revoked link cannot be restored; generate a new report to share again."
        confirmLabel="Revoke link"
        pendingLabel="Revoking"
        pending={revoke.isPending}
        error={revoke.error}
        onConfirm={() => revoking && revoke.mutate(revoking.id)}
      />
    </div>
  );
}
