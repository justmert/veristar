import {
  type QueryClient,
  useMutation,
  useQueries,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { Download, ListChecks, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { ApiError } from "@/api/client";
import {
  type CohortValue,
  cohortListQuery,
  type Rule,
  type RuleInput,
  type RuleMatches,
  type RuleMetric,
  ruleApi,
  ruleMatchesCsv,
  ruleMatchesQuery,
  rulesQuery,
} from "@/api/cohorts";
import { CreateDialog } from "@/components/create-dialog";
import { type Column, DataTable } from "@/components/data-table";
import { SelectField, TextField } from "@/components/form-field";
import { PageHeader } from "@/components/page-header";
import { DateField, isIsoDay } from "@/components/range-control";
import { EmptyState, ErrorState, LoadingState, Missing, Skeleton } from "@/components/states";
import { toast } from "@/components/toast";
import { Button } from "@/components/ui/button";
import { countOf, formatCount, formatDate, formatDateRange } from "@/lib/format";
import { isDay } from "@/lib/global";
import { fieldMessage } from "@/lib/validate";
import { cohortText } from "./shared";

// saved threshold rules (contract 19.4): an operator defines a traction
// rule once and lists the projects that meet it, each with its evidence

export const METRIC_TEXT: Record<RuleMetric, string> = {
  visitors: "Verified visitors",
  requests: "API requests",
  onchain_operations: "On-chain operations",
};

// the rule in one sentence, the way the evaluation reads it
export function describeRule(
  r: Pick<Rule, "metric" | "min_per_day" | "min_days" | "months" | "min_grade">,
): string {
  const months =
    r.months === 1 ? "the last complete month" : `each of the last ${r.months} complete months`;
  const grade = r.min_grade ? `, integrity grade ${r.min_grade} or better` : "";
  return `${METRIC_TEXT[r.metric]} of at least ${formatCount(r.min_per_day)} a day on ${r.min_days} or more days in ${months}${grade}`;
}

export type RuleForm = {
  name: string;
  metric: RuleMetric;
  minPerDay: string;
  minDays: string;
  months: string;
  minGrade: "" | "A" | "B" | "C";
  program: string;
  round: string;
  category: string;
};

export const EMPTY_RULE: RuleForm = {
  name: "",
  metric: "visitors",
  minPerDay: "",
  minDays: "",
  months: "3",
  minGrade: "",
  program: "",
  round: "",
  category: "",
};

function formOf(r: Rule): RuleForm {
  return {
    name: r.name,
    metric: r.metric,
    minPerDay: String(r.min_per_day),
    minDays: String(r.min_days),
    months: String(r.months),
    minGrade: r.min_grade ?? "",
    program: r.cohort.program ?? "",
    round: r.cohort.round ?? "",
    category: r.cohort.category ?? "",
  };
}

const int = (s: string) => (/^\d+$/.test(s.trim()) ? Number(s.trim()) : Number.NaN);

// the bounds of 19.4, checked before sending; the server checks them again
export function validateRule(f: RuleForm): {
  errors: Partial<Record<keyof RuleForm, string>>;
  body: RuleInput | null;
} {
  const errors: Partial<Record<keyof RuleForm, string>> = {};
  const name = f.name.trim();
  if (name === "") errors.name = "Required.";
  else if (name.length > 100) errors.name = "At most 100 characters.";
  const perDay = int(f.minPerDay);
  if (!(perDay >= 1 && perDay <= 1_000_000_000_000))
    errors.minPerDay = "A whole number of at least 1.";
  const days = int(f.minDays);
  if (!(days >= 1 && days <= 28)) errors.minDays = "A whole number from 1 to 28.";
  const months = int(f.months);
  if (!(months >= 1 && months <= 12)) errors.months = "A whole number from 1 to 12.";
  for (const k of ["program", "round", "category"] as const) {
    if (f[k].trim().length > 80) errors[k] = "At most 80 characters.";
  }
  if (Object.keys(errors).length > 0) return { errors, body: null };
  const opt = (s: string) => (s.trim() === "" ? null : s.trim());
  return {
    errors,
    body: {
      name,
      metric: f.metric,
      min_per_day: perDay,
      min_days: days,
      months,
      min_grade: f.minGrade === "" ? null : f.minGrade,
      cohort: { program: opt(f.program), round: opt(f.round), category: opt(f.category) },
    },
  };
}

const SERVER_FIELDS: Record<string, keyof RuleForm> = {
  name: "name",
  metric: "metric",
  min_per_day: "minPerDay",
  min_days: "minDays",
  months: "months",
  min_grade: "minGrade",
  "cohort.program": "program",
  "cohort.round": "round",
  "cohort.category": "category",
};

function refreshRules(qc: QueryClient) {
  return qc.invalidateQueries({ queryKey: ["operator", "rules"] });
}

export function RulesPage() {
  const [params, setParams] = useSearchParams();
  const selected = params.get("rule") ?? "";
  const asOfParam = params.get("as_of") ?? "";
  const asOf = isDay(asOfParam) ? asOfParam : "";
  const rules = useQuery(rulesQuery);
  const items = rules.data?.items ?? [];
  // each rule's match count, evaluated today, so the list says how many
  // projects meet it without opening it
  const counts = useQueries({ queries: items.map((r) => ruleMatchesQuery(r.id, "")) });
  const qc = useQueryClient();
  const [editing, setEditing] = useState<{ id: string | null; form: RuleForm } | null>(null);
  async function remove(r: Rule) {
    await ruleApi.remove(r.id);
    if (r.id === selected) setParams({}, { replace: true });
    toast.success(`${r.name} is deleted.`);
    await refreshRules(qc);
  }
  const open = (id: string) => setParams({ rule: id }, { replace: true });
  const current = items.find((r) => r.id === selected) ?? null;

  const columns: Column<Rule>[] = [
    {
      key: "rule",
      header: "Rule",
      primary: true,
      minWidth: "min-w-64",
      cell: (r) => (
        <span className="flex min-w-0 flex-col">
          <span className="truncate font-medium text-fg">{r.name}</span>
          <span className="text-caption font-normal whitespace-normal text-fg-2">
            {describeRule(r)}
          </span>
        </span>
      ),
    },
    {
      key: "cohort",
      header: "Cohort",
      mobile: true,
      cell: (r) => cohortText(r.cohort) ?? "Every project",
    },
    {
      key: "matches",
      header: "Projects that meet it",
      definition: "Projects that meet the rule when it is evaluated today.",
      align: "right",
      numeric: true,
      mobile: true,
      cell: (r) => {
        const q = counts[items.indexOf(r)];
        if (!q || q.isPending) return <span className="text-fg-2">Evaluating</span>;
        if (q.error || !q.data) return <Missing reason="The rule did not evaluate" />;
        return (
          <span data-matches={r.id}>
            {formatCount(q.data.items.length)} of {formatCount(q.data.evaluated)}
          </span>
        );
      },
    },
  ];

  return (
    <div className="flex min-w-0 flex-col gap-6">
      <PageHeader
        title="Rules"
        primary={
          <RuleDialog
            key={editing?.id ?? (editing ? "new" : "closed")}
            id={editing?.id ?? null}
            initial={editing?.form ?? EMPTY_RULE}
            open={editing !== null}
            onOpenChange={(o) => {
              if (o) setEditing((e) => e ?? { id: null, form: EMPTY_RULE });
              else setEditing(null);
            }}
            onDone={(saved) => {
              setEditing(null);
              if (saved) open(saved.id);
            }}
          />
        }
      />

      {rules.isPending ? (
        <LoadingState label="Loading the rules">
          <Skeleton.Table rows={3} columns={3} />
        </LoadingState>
      ) : rules.error ? (
        <ErrorState
          title="The rules did not load"
          error={rules.error}
          onRetry={() => rules.refetch()}
        />
      ) : items.length === 0 ? (
        <EmptyState icon={<ListChecks aria-hidden />} title="No rule is saved yet" />
      ) : (
        <DataTable
          label="Rules"
          columns={columns}
          rows={items}
          getRowKey={(r) => r.id}
          rowLabel={(r) => r.name}
          rowHref={(r) => `/operator/rules?rule=${r.id}`}
          rowProps={(r) => ({
            "data-rule": r.id,
            ...(r.id === selected ? { "data-current": "" } : {}),
          })}
          rowActions={(r) => [
            { label: "Edit rule", onSelect: () => setEditing({ id: r.id, form: formOf(r) }) },
            {
              label: "Delete rule",
              icon: <Trash2 aria-hidden />,
              destructive: true,
              confirm: {
                title: `Delete the rule “${r.name}”?`,
                description:
                  "The rule goes away for every operator, the machine API and the AI tools. The audit log keeps its definition.",
                confirmLabel: "Delete rule",
                pendingLabel: "Deleting",
                run: () => remove(r),
              },
            },
          ]}
        />
      )}

      {current ? (
        <Matches
          rule={current}
          asOf={asOf}
          onAsOf={(d) =>
            setParams(d ? { rule: current.id, as_of: d } : { rule: current.id }, { replace: true })
          }
        />
      ) : null}
    </div>
  );
}

const COHORT_LIST = { program: "programs", round: "rounds", category: "categories" } as const;
const COHORT_LABEL = {
  program: "SCF program",
  round: "SCF round",
  category: "SCF category",
} as const;

function CohortSelect({
  field,
  values,
  loading,
  value,
  onChange,
  error,
}: {
  field: "program" | "round" | "category";
  values: CohortValue[];
  loading: boolean;
  value: string;
  onChange: (e: React.ChangeEvent<HTMLSelectElement>) => void;
  error?: string;
}) {
  const opts = values.map((v) => v.value);
  // a rule saved with a value no project holds any more keeps showing it
  if (value && !opts.includes(value)) opts.unshift(value);
  return (
    <SelectField
      label={COHORT_LABEL[field]}
      value={value}
      onChange={onChange}
      error={error}
      disabled={loading}
      className="w-full"
    >
      <option value="">{loading ? "Loading" : "Any"}</option>
      {opts.map((o) => (
        <option key={o} value={o}>
          {o}
        </option>
      ))}
    </SelectField>
  );
}

// the header button and the rule form in its dialog, for a new rule or
// for one picked from a row menu
function RuleDialog({
  id,
  initial,
  open,
  onOpenChange,
  onDone,
}: {
  id: string | null;
  initial: RuleForm;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onDone: (saved: Rule | null) => void;
}) {
  const qc = useQueryClient();
  const [form, setForm] = useState<RuleForm>(initial);
  const [errors, setErrors] = useState<Partial<Record<keyof RuleForm, string>>>({});
  // a rule's cohort matches the values operators confirmed (19.1); free text
  // could name a cohort no project holds, so the choice is from those values
  const cohorts = useQuery(cohortListQuery);
  const save = useMutation({
    mutationFn: (body: RuleInput) => (id ? ruleApi.update(id, body) : ruleApi.create(body)),
    onSuccess: async (r) => {
      await refreshRules(qc);
      toast.success(`${r.name} is saved.`);
      onDone(r);
    },
    onError: (err) => {
      if (!(err instanceof ApiError) || err.code !== "validation_failed") return;
      const out: Partial<Record<keyof RuleForm, string>> = {};
      for (const [path, code] of Object.entries(err.fields)) {
        const k = SERVER_FIELDS[path];
        if (k) out[k] = fieldMessage(code);
      }
      setErrors(out);
    },
  });
  const set = (k: keyof RuleForm) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
    setForm((f) => ({ ...f, [k]: e.target.value }));

  function submit() {
    const v = validateRule(form);
    setErrors(v.errors);
    if (v.body) save.mutate(v.body);
  }

  // two fields to a row, each row the same height: the labels are short
  // enough never to wrap, and a hint sits under both fields of a row or
  // under neither. the optional fields are one group, marked once.
  return (
    <CreateDialog
      label="New rule"
      title={id ? "Edit rule" : "New rule"}
      submitLabel="Save rule"
      pendingLabel="Saving"
      pending={save.isPending}
      error={
        save.error instanceof ApiError && save.error.code === "validation_failed"
          ? null
          : save.error
      }
      onSubmit={submit}
      open={open}
      onOpenChange={onOpenChange}
      onClosed={() => onDone(null)}
    >
      <TextField
        label="Name"
        value={form.name}
        onChange={set("name")}
        error={errors.name}
        maxLength={100}
      />
      <div className="grid gap-4 sm:grid-cols-2">
        <SelectField
          label="Metric"
          value={form.metric}
          onChange={set("metric")}
          error={errors.metric}
          className="w-full"
        >
          {(Object.keys(METRIC_TEXT) as RuleMetric[]).map((m) => (
            <option key={m} value={m}>
              {METRIC_TEXT[m]}
            </option>
          ))}
        </SelectField>
        <TextField
          label="Minimum per day"
          inputMode="numeric"
          value={form.minPerDay}
          onChange={set("minPerDay")}
          error={errors.minPerDay}
        />
        <TextField
          label="Days per month"
          inputMode="numeric"
          hint="1 to 28"
          value={form.minDays}
          onChange={set("minDays")}
          error={errors.minDays}
        />
        <TextField
          label="Complete months"
          inputMode="numeric"
          hint="1 to 12"
          value={form.months}
          onChange={set("months")}
          error={errors.months}
        />
      </div>
      <fieldset className="flex flex-col gap-3 border-t border-line pt-4">
        <legend className="float-left mb-3 text-small text-fg-2">Optional</legend>
        <div className="clear-left grid gap-4 sm:grid-cols-2">
          <SelectField
            label="Integrity grade"
            value={form.minGrade}
            onChange={set("minGrade")}
            error={errors.minGrade}
            className="w-full"
          >
            <option value="">Any grade</option>
            <option value="A">A</option>
            <option value="B">B or better</option>
            <option value="C">C or better</option>
          </SelectField>
          {(["program", "round", "category"] as const).map((k) => (
            <CohortSelect
              key={k}
              field={k}
              values={cohorts.data?.[COHORT_LIST[k]] ?? []}
              loading={cohorts.isPending}
              value={form[k]}
              onChange={set(k)}
              error={
                errors[k] ??
                (cohorts.error ? "The cohort values did not load. Reload the page." : undefined)
              }
            />
          ))}
        </div>
      </fieldset>
    </CreateDialog>
  );
}

function Matches({
  rule,
  asOf,
  onAsOf,
}: {
  rule: Rule;
  asOf: string;
  onAsOf: (d: string) => void;
}) {
  const q = useQuery(ruleMatchesQuery(rule.id, asOf));
  const [draft, setDraft] = useState(asOf);
  useEffect(() => setDraft(asOf), [asOf]);
  return (
    <section
      aria-label="Rule matches"
      className="flex min-w-0 flex-col gap-4 rounded-lg border border-line bg-raised p-4 sm:p-5"
    >
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div className="flex min-w-0 flex-col gap-1">
          <h2 className="text-title break-words text-fg">Projects that meet {rule.name}</h2>
          <p className="text-small text-fg-2">{describeRule(rule)}.</p>
        </div>
        <div className="flex flex-wrap items-end gap-2">
          <div className="w-44">
            <DateField
              label="Evaluated on"
              optional
              value={draft}
              onChange={(v) => {
                setDraft(v);
                const t = v.trim();
                if (t !== asOf && (t === "" || isIsoDay(t))) onAsOf(t);
              }}
            />
          </div>
          <Button
            asChild
            variant="outline"
            size="sm"
            className="mb-6 max-lg:h-(--vs-control-touch)"
          >
            <a href={ruleMatchesCsv(rule.id, asOf)} download>
              <Download aria-hidden />
              Export CSV
            </a>
          </Button>
        </div>
      </div>
      {q.isPending ? (
        <LoadingState label="Evaluating the rule">
          <Skeleton.Table rows={3} columns={2} />
        </LoadingState>
      ) : q.error ? (
        <ErrorState title="The rule did not evaluate" error={q.error} onRetry={() => q.refetch()} />
      ) : (
        <MatchList data={q.data} />
      )}
    </section>
  );
}

function MatchList({ data }: { data: RuleMatches }) {
  return (
    <div className="flex flex-col gap-3">
      <p className="text-small text-fg-2" aria-live="polite">
        {formatCount(data.items.length)} of {countOf(data.evaluated, "project")} meet the rule.
        Months: {data.months.join(", ")}. Days {formatDateRange(data.from, data.to)}, UTC.
      </p>
      {data.items.length === 0 ? null : (
        <ul className="flex flex-col divide-y divide-line" aria-label="Projects that meet the rule">
          {data.items.map((m) => (
            <li
              key={m.tenant.id}
              data-tenant={m.tenant.id}
              className="flex min-w-0 flex-col gap-1 py-3"
            >
              <span className="flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-1">
                <Link
                  to={`/operator/projects/${m.tenant.id}`}
                  className="min-w-0 truncate rounded-sm text-body font-medium text-fg underline-offset-4 hover:underline focus-ring"
                >
                  {m.tenant.name}
                </Link>
                <span className="text-caption text-fg-2">
                  {m.integrity.grade ? `Grade ${m.integrity.grade}` : "No grade"},{" "}
                  {countOf(m.integrity.graded_days, "graded site day")}
                </span>
                {cohortText(m.cohort) ? (
                  <span className="text-caption text-fg-2">{cohortText(m.cohort)}</span>
                ) : null}
              </span>
              <ul className="flex flex-col gap-1 text-caption text-fg-2">
                {m.months.map((mo) => (
                  <li key={mo.month} data-month={mo.month} className="break-words">
                    <span className="font-medium text-fg">{mo.month}</span>:{" "}
                    {countOf(mo.qualifying_days, "qualifying day")}
                    {mo.days.length > 0
                      ? ` (${mo.days.map((d) => `${formatDate(d.day)}: ${formatCount(d.value)}`).join(", ")})`
                      : ""}
                  </li>
                ))}
              </ul>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
