// the cohort a project is compared in (contract 19.1): program, round,
// category and award date. a project proposes them, an operator confirms
// them, and only confirmed values place a project in a cohort.

import { type QueryClient, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { type FormEvent, useState } from "react";
import {
  type Cohort,
  type CohortInput,
  type CohortRecord,
  cohortApi,
  cohortKeys,
  operatorCohortQuery,
} from "@/api/cohorts";
import { FormMessage } from "@/components/error-panel";
import { TextField } from "@/components/form-field";
import { Fact, Panel } from "@/components/panel";
import { DateField } from "@/components/range-control";
import { ErrorState, LoadingState, Skeleton } from "@/components/states";
import { Pill } from "@/components/status";
import { Button } from "@/components/ui/button";
import { formatDate } from "@/lib/format";

// a percentile places a project among at least this many projects; in a
// smaller cohort one project more or less moves it by a quarter or more
export const PERCENTILE_MIN_COHORT = 5;

export function percentileHiddenReason(cohortSize: number): string | null {
  return cohortSize < PERCENTILE_MIN_COHORT
    ? `Fewer than ${PERCENTILE_MIN_COHORT} projects in the cohort`
    : null;
}

export type CohortValues = { program: string; round: string; category: string; award_date: string };

export const EMPTY_COHORT: CohortValues = { program: "", round: "", category: "", award_date: "" };

export function valuesOf(c: Cohort | null): CohortValues {
  return {
    program: c?.program ?? "",
    round: c?.round ?? "",
    category: c?.category ?? "",
    award_date: c?.award_date ?? "",
  };
}

// the body of a cohort change: trimmed values, an empty field sent as null
// so an operator can clear it
export function cohortBody(v: CohortValues): CohortInput {
  const t = (s: string) => (s.trim() === "" ? null : s.trim());
  return {
    program: t(v.program),
    round: t(v.round),
    category: t(v.category),
    award_date: t(v.award_date),
  };
}

export function isEmptyCohort(v: CohortValues): boolean {
  return Object.values(v).every((x) => x.trim() === "");
}

// the checks of contract 19.1 the form can make before sending; the server
// makes them again
export function validateCohort(
  v: CohortValues,
  today: string,
): Partial<Record<keyof CohortValues, string>> {
  const out: Partial<Record<keyof CohortValues, string>> = {};
  for (const k of ["program", "round", "category"] as const) {
    const s = v[k].trim();
    if (s.length > 80) out[k] = "At most 80 characters.";
    else if (/[\t\n\r]/.test(s)) out[k] = "One line only.";
  }
  const d = v.award_date.trim();
  if (d !== "") {
    if (!/^\d{4}-\d{2}-\d{2}$/.test(d)) out.award_date = "Enter a day.";
    else if (d < "2018-01-01" || d > today) out.award_date = "A day from 2018 up to today.";
  }
  return out;
}

export function CohortFields({
  values,
  onChange,
  errors = {},
  idPrefix,
}: {
  values: CohortValues;
  onChange: (v: CohortValues) => void;
  errors?: Partial<Record<keyof CohortValues, string>>;
  idPrefix: string;
}) {
  const set = (k: keyof CohortValues) => (e: React.ChangeEvent<HTMLInputElement>) =>
    onChange({ ...values, [k]: e.target.value });
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      <TextField
        label="SCF program"
        name={`${idPrefix}-program`}
        optional
        placeholder="Build Award"
        maxLength={80}
        value={values.program}
        onChange={set("program")}
        error={errors.program}
      />
      <TextField
        label="Round"
        name={`${idPrefix}-round`}
        optional
        placeholder="SCF #38"
        maxLength={80}
        value={values.round}
        onChange={set("round")}
        error={errors.round}
      />
      <TextField
        label="Category"
        name={`${idPrefix}-category`}
        optional
        placeholder="DeFi"
        maxLength={80}
        value={values.category}
        onChange={set("category")}
        error={errors.category}
      />
      <DateField
        label="Award date"
        name={`${idPrefix}-award_date`}
        optional
        value={values.award_date}
        onChange={(v) => onChange({ ...values, award_date: v })}
        error={errors.award_date}
      />
    </div>
  );
}

function CohortFacts({ c }: { c: Cohort }) {
  return (
    // four columns only where the card is wide, not in a side column
    <div className="@container">
      <dl className="grid grid-cols-2 gap-3 @lg:grid-cols-4">
        <Fact label="Program">{c.program ?? "Not set"}</Fact>
        <Fact label="Round">{c.round ?? "Not set"}</Fact>
        <Fact label="Category">{c.category ?? "Not set"}</Fact>
        <Fact label="Award date">{c.award_date ? formatDate(c.award_date) : "Not set"}</Fact>
      </dl>
    </div>
  );
}

const today = () => new Date().toISOString().slice(0, 10);

function refreshCohort(qc: QueryClient, tenantId: string, record: CohortRecord) {
  qc.setQueryData(cohortKeys.operator(tenantId), record);
  // the lists, filters and the compare page read the confirmed values
  return qc.invalidateQueries({ queryKey: ["operator"] });
}

// the state of the cohort as one badge: a proposal that waits for an
// operator, or confirmed values
function CohortBadge({ record }: { record: CohortRecord }) {
  if (record.proposal) return <Pill tone="info">Proposed</Pill>;
  if (record.confirmed) return <Pill tone="success">Confirmed</Pill>;
  return null;
}

// the operator's cohort panel of a project: the confirmed values, the
// project's pending proposal with confirm and dismiss, and an edit form.
// readOnly shows the values only, for a project that is removed.
export function OperatorCohortPanel({
  tenantId,
  readOnly = false,
}: {
  tenantId: string;
  readOnly?: boolean;
}) {
  const q = useQuery(operatorCohortQuery(tenantId));
  const qc = useQueryClient();
  const [editing, setEditing] = useState<CohortValues | null>(null);
  const [errors, setErrors] = useState<Partial<Record<keyof CohortValues, string>>>({});
  const save = useMutation({
    mutationFn: (v: CohortValues) => cohortApi.set(tenantId, cohortBody(v)),
    onSuccess: async (r) => {
      setEditing(null);
      await refreshCohort(qc, tenantId, r);
    },
  });
  const confirm = useMutation({
    mutationFn: () => cohortApi.confirm(tenantId),
    onSuccess: (r) => refreshCohort(qc, tenantId, r),
  });
  const dismiss = useMutation({
    mutationFn: () => cohortApi.dismiss(tenantId),
    onSuccess: (r) => refreshCohort(qc, tenantId, r),
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    if (!editing) return;
    const found = validateCohort(editing, today());
    setErrors(found);
    if (Object.keys(found).length === 0) save.mutate(editing);
  }

  return (
    <Panel
      title="SCF cohort"
      aria-label="SCF cohort"
      actions={
        q.data ? (
          <>
            <CohortBadge record={q.data} />
            {editing || readOnly ? null : (
              <Button
                variant="outline"
                size="sm"
                onClick={() => setEditing(valuesOf(q.data.confirmed))}
              >
                Edit
              </Button>
            )}
          </>
        ) : null
      }
    >
      {q.isPending ? (
        <LoadingState label="Loading the cohort">
          <Skeleton.Table rows={1} columns={4} />
        </LoadingState>
      ) : null}
      {q.error ? (
        <ErrorState
          compact
          title="The cohort did not load"
          error={q.error}
          onRetry={() => q.refetch()}
        />
      ) : null}
      {q.data ? (
        <div className="flex flex-col gap-4">
          {editing ? (
            <form onSubmit={submit} className="flex flex-col gap-3" noValidate>
              <CohortFields
                values={editing}
                onChange={setEditing}
                errors={errors}
                idPrefix="op-cohort"
              />
              <FormMessage error={save.error} />
              <div className="flex gap-2">
                <Button type="submit" pending={save.isPending}>
                  {save.isPending ? "Saving" : "Save cohort"}
                </Button>
                <Button type="button" variant="ghost" onClick={() => setEditing(null)}>
                  Cancel
                </Button>
              </div>
              <p className="text-caption text-fg-3">Leave every field empty to clear the cohort.</p>
            </form>
          ) : q.data.confirmed ? (
            <CohortFacts c={q.data.confirmed} />
          ) : (
            <p className="text-body text-fg-2">No cohort is confirmed for this project.</p>
          )}
          {q.data.proposal && !readOnly ? (
            <div data-proposal className="flex flex-col gap-3 border-t border-line-subtle pt-4">
              <div className="flex flex-wrap items-center gap-2">
                <Pill tone="info">Proposed by the project</Pill>
                {q.data.proposed_at ? (
                  <span className="text-caption text-fg-3">
                    {formatDate(q.data.proposed_at.slice(0, 10))}
                  </span>
                ) : null}
              </div>
              <CohortFacts c={q.data.proposal} />
              <FormMessage error={confirm.error ?? dismiss.error} />
              <div className="flex flex-wrap gap-2">
                <Button size="sm" onClick={() => confirm.mutate()} pending={confirm.isPending}>
                  Confirm proposal
                </Button>
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => dismiss.mutate()}
                  pending={dismiss.isPending}
                >
                  Dismiss proposal
                </Button>
              </div>
            </div>
          ) : null}
        </div>
      ) : null}
    </Panel>
  );
}

// the member's view of the project's cohort, from the record the "what scf
// sees" answer carries: what operators confirmed, the pending proposal, and
// for an owner a form to propose one
export function OwnCohortPanel({
  tenantId,
  record,
  isOwner,
}: {
  tenantId: string;
  record: CohortRecord;
  isOwner: boolean;
}) {
  const queryClient = useQueryClient();
  const [values, setValues] = useState<CohortValues | null>(null);
  const [errors, setErrors] = useState<Partial<Record<keyof CohortValues, string>>>({});
  const propose = useMutation({
    mutationFn: (v: CohortValues) => cohortApi.propose(tenantId, cohortBody(v)),
    onSuccess: async (r) => {
      setValues(null);
      queryClient.setQueryData(cohortKeys.own(tenantId), r);
      await queryClient.invalidateQueries({ queryKey: ["tenant", tenantId, "scf-view"] });
    },
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    if (!values) return;
    const found = validateCohort(values, today());
    if (isEmptyCohort(values)) found.program = "Fill in at least one field.";
    setErrors(found);
    if (Object.keys(found).length === 0) propose.mutate(values);
  }

  // one status word in the header: proposed while an operator has not
  // decided, confirmed once one did
  const badge = record.proposal ? (
    <Pill tone="info">Proposed</Pill>
  ) : record.confirmed ? (
    <Pill tone="success">Confirmed</Pill>
  ) : null;
  const hasAny = record.confirmed !== null || record.proposal !== null;
  return (
    <Panel
      title={
        <span className="inline-flex flex-wrap items-center gap-2">
          SCF cohort
          {badge}
        </span>
      }
      aria-label="SCF cohort"
      actions={
        isOwner && !values ? (
          <Button
            variant="outline"
            size="sm"
            onClick={() => setValues(valuesOf(record.proposal ?? record.confirmed))}
          >
            {hasAny ? "Edit" : "Propose a cohort"}
          </Button>
        ) : null
      }
    >
      <div className="flex flex-col gap-4">
        {record.confirmed ? (
          <CohortFacts c={record.confirmed} />
        ) : record.proposal ? null : (
          <p className="text-body text-fg-2">No cohort is confirmed for your project yet.</p>
        )}
        {record.proposal && !values ? (
          <div data-proposal className="flex flex-col gap-2">
            {record.confirmed ? (
              <span className="text-caption text-fg-2">Your proposal, waiting for an operator</span>
            ) : null}
            <CohortFacts c={record.proposal} />
          </div>
        ) : null}
        {values ? (
          <form onSubmit={submit} className="flex flex-col gap-3" noValidate>
            <CohortFields
              values={values}
              onChange={setValues}
              errors={errors}
              idPrefix="own-cohort"
            />
            <FormMessage error={propose.error} />
            <div className="flex gap-2">
              <Button type="submit" pending={propose.isPending}>
                {propose.isPending ? "Sending" : "Send proposal"}
              </Button>
              <Button type="button" variant="ghost" onClick={() => setValues(null)}>
                Cancel
              </Button>
            </div>
          </form>
        ) : null}
      </div>
    </Panel>
  );
}
