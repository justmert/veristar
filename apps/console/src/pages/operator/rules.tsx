import { type QueryClient, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Download, ListChecks, Plus } from "lucide-react";
import { type FormEvent, useState } from "react";
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
import { ConfirmDialog } from "@/components/confirm-dialog";
import { ErrorPanel, FormMessage, Loading, StateBlock } from "@/components/error-panel";
import { SelectField, TextField } from "@/components/form-field";
import { Panel } from "@/components/panel";
import { Button } from "@/components/ui/button";
import { countOf, formatCount, formatDateRange } from "@/lib/format";
import { isDay } from "@/lib/global";
import { fieldMessage } from "@/lib/validate";
import { cohortText, TD, TH } from "./global-shared";

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
  const qc = useQueryClient();
  const [editing, setEditing] = useState<{ id: string | null; form: RuleForm } | null>(null);
  const [deleting, setDeleting] = useState<Rule | null>(null);
  const remove = useMutation({
    mutationFn: (id: string) => ruleApi.remove(id),
    onSuccess: async (_d, id) => {
      setDeleting(null);
      if (id === selected) setParams({}, { replace: true });
      await refreshRules(qc);
    },
  });
  const open = (id: string) => setParams({ rule: id }, { replace: true });
  const current = rules.data?.items.find((r) => r.id === selected) ?? null;

  return (
    <section aria-labelledby="rules-h" className="flex flex-col gap-5">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex flex-col gap-1">
          <h2 id="rules-h" className="text-section text-fg">
            Threshold rules
          </h2>
          <p className="max-w-prose text-small text-fg-2">
            Define a traction rule once, such as verified visitors on enough days of every month,
            and list the projects that meet it with the days that qualify them and their integrity
            grade. Months are complete UTC calendar months before the evaluation day.
          </p>
        </div>
        {editing ? null : (
          <Button onClick={() => setEditing({ id: null, form: EMPTY_RULE })}>
            <Plus aria-hidden />
            New rule
          </Button>
        )}
      </div>

      {editing ? (
        <RuleEditor
          id={editing.id}
          initial={editing.form}
          onDone={(saved) => {
            setEditing(null);
            if (saved) open(saved.id);
          }}
        />
      ) : null}

      {rules.isPending ? <Loading label="Loading rules" /> : null}
      {rules.error ? <ErrorPanel error={rules.error} onRetry={() => rules.refetch()} /> : null}
      {rules.data ? (
        rules.data.items.length === 0 ? (
          <StateBlock kind="empty" icon={<ListChecks aria-hidden />} title="No rule is saved yet">
            Save a rule to list the projects that meet it.
          </StateBlock>
        ) : (
          <div className="overflow-x-auto rounded-lg border border-line bg-raised">
            <table className="w-full min-w-[48rem] border-separate border-spacing-0 text-left text-small">
              <thead>
                <tr>
                  <th scope="col" className={`${TH} pl-4`}>
                    Rule
                  </th>
                  <th scope="col" className={TH}>
                    Cohort
                  </th>
                  <th scope="col" className={`${TH} pr-4 text-right`}>
                    <span className="sr-only">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody className="[&>tr:last-child>*]:border-0">
                {rules.data.items.map((r) => (
                  <tr
                    key={r.id}
                    data-rule={r.id}
                    data-state={r.id === selected ? "selected" : undefined}
                    className="data-[state=selected]:bg-selected"
                  >
                    <th scope="row" className={`${TD} pl-4 font-normal`}>
                      <span className="block text-body font-medium text-fg">{r.name}</span>
                      <span className="block text-caption text-fg-3">{describeRule(r)}</span>
                    </th>
                    <td className={`${TD} text-caption text-fg-2`}>
                      {cohortText({ ...r.cohort, award_date: null }) ?? "Every project"}
                    </td>
                    <td className={`${TD} pr-4`}>
                      <span className="flex justify-end gap-1">
                        <Button size="sm" variant="outline" onClick={() => open(r.id)}>
                          Matches
                        </Button>
                        <Button
                          size="sm"
                          variant="ghost"
                          onClick={() => setEditing({ id: r.id, form: formOf(r) })}
                        >
                          Edit
                        </Button>
                        <Button size="sm" variant="ghost" onClick={() => setDeleting(r)}>
                          Delete
                        </Button>
                      </span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )
      ) : null}

      {current ? (
        <Matches
          rule={current}
          asOf={asOf}
          onAsOf={(d) =>
            setParams(d ? { rule: current.id, as_of: d } : { rule: current.id }, { replace: true })
          }
        />
      ) : null}

      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(o) => {
          if (!o) {
            setDeleting(null);
            // a refusal shown for one rule must not greet the next
            remove.reset();
          }
        }}
        title={`Delete ${deleting?.name ?? "the rule"}?`}
        description="The rule is removed for every operator, the machine API and the AI tools. The audit log keeps its definition."
        confirmLabel="Delete rule"
        pendingLabel="Deleting"
        pending={remove.isPending}
        error={remove.error}
        onConfirm={() => deleting && remove.mutate(deleting.id)}
      />
    </section>
  );
}

const COHORT_LIST = { program: "programs", round: "rounds", category: "categories" } as const;
const COHORT_LABEL = { program: "Program", round: "Round", category: "Category" } as const;

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

function RuleEditor({
  id,
  initial,
  onDone,
}: {
  id: string | null;
  initial: RuleForm;
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

  function submit(e: FormEvent) {
    e.preventDefault();
    const v = validateRule(form);
    setErrors(v.errors);
    if (v.body) save.mutate(v.body);
  }

  return (
    <Panel title={id ? "Edit rule" : "New rule"} aria-label={id ? "Edit rule" : "New rule"}>
      <form onSubmit={submit} noValidate className="flex flex-col gap-4">
        <TextField
          label="Name"
          value={form.name}
          onChange={set("name")}
          error={errors.name}
          maxLength={100}
          required
        />
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
          <SelectField
            label="Metric"
            value={form.metric}
            onChange={set("metric")}
            error={errors.metric}
          >
            {(Object.keys(METRIC_TEXT) as RuleMetric[]).map((m) => (
              <option key={m} value={m}>
                {METRIC_TEXT[m]}
              </option>
            ))}
          </SelectField>
          <TextField
            label="At least, per day"
            inputMode="numeric"
            value={form.minPerDay}
            onChange={set("minPerDay")}
            error={errors.minPerDay}
            required
          />
          <TextField
            label="On days per month"
            inputMode="numeric"
            hint="1 to 28"
            value={form.minDays}
            onChange={set("minDays")}
            error={errors.minDays}
            required
          />
          <TextField
            label="Months"
            inputMode="numeric"
            hint="1 to 12 complete months"
            value={form.months}
            onChange={set("months")}
            error={errors.months}
            required
          />
        </div>
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
          <SelectField
            label="Minimum integrity grade"
            value={form.minGrade}
            onChange={set("minGrade")}
            error={errors.minGrade}
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
                (cohorts.error
                  ? "The confirmed values could not be loaded. Reload the page."
                  : undefined)
              }
            />
          ))}
        </div>
        <FormMessage
          error={
            save.error instanceof ApiError && save.error.code === "validation_failed"
              ? null
              : save.error
          }
        />
        <div className="flex gap-2">
          <Button type="submit" pending={save.isPending}>
            {save.isPending ? "Saving" : "Save rule"}
          </Button>
          <Button type="button" variant="ghost" onClick={() => onDone(null)}>
            Cancel
          </Button>
        </div>
      </form>
    </Panel>
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
  return (
    <Panel
      title={`Projects that meet ${rule.name}`}
      aria-label="Rule matches"
      description={describeRule(rule)}
      actions={
        <span className="flex flex-wrap items-end gap-2">
          <TextField
            label="Evaluated on"
            type="date"
            value={asOf}
            onChange={(e) => onAsOf(e.target.value)}
            className="h-(--vs-control-sm)"
          />
          <Button asChild variant="outline" size="sm">
            <a href={ruleMatchesCsv(rule.id, asOf)} download>
              <Download aria-hidden />
              Export CSV
            </a>
          </Button>
        </span>
      }
    >
      {q.isPending ? <Loading label="Evaluating the rule" /> : null}
      {q.error ? <ErrorPanel error={q.error} onRetry={() => q.refetch()} /> : null}
      {q.data ? <MatchList data={q.data} /> : null}
    </Panel>
  );
}

function MatchList({ data }: { data: RuleMatches }) {
  return (
    <div className="flex flex-col gap-3">
      <p className="text-small text-fg-2" aria-live="polite">
        {data.items.length} of {data.evaluated} evaluated projects meet the rule, over{" "}
        {formatDateRange(data.from, data.to)} ({data.months.join(", ")}).
      </p>
      {data.items.length === 0 ? null : (
        <ul className="flex flex-col divide-y divide-line" aria-label="Projects that meet the rule">
          {data.items.map((m) => (
            <li key={m.tenant.id} data-tenant={m.tenant.id} className="flex flex-col gap-1 py-3">
              <span className="flex flex-wrap items-baseline gap-x-3">
                <Link
                  to={`/operator/projects/${m.tenant.id}`}
                  className="rounded-sm text-body font-medium text-fg underline-offset-4 hover:underline focus-ring"
                >
                  {m.tenant.name}
                </Link>
                <span className="text-caption text-fg-3">
                  {m.integrity.grade ? `Integrity grade ${m.integrity.grade}` : "Not graded"} (
                  {countOf(m.integrity.graded_days, "graded site day")})
                </span>
                {cohortText(m.cohort) ? (
                  <span className="text-caption text-fg-3">{cohortText(m.cohort)}</span>
                ) : null}
              </span>
              <ul className="flex flex-col gap-1 text-caption text-fg-2">
                {m.months.map((mo) => (
                  <li key={mo.month} data-month={mo.month}>
                    <span className="font-medium text-fg">{mo.month}</span>:{" "}
                    {countOf(mo.qualifying_days, "qualifying day")}
                    {mo.days.length > 0 ? (
                      <span className="text-fg-3">
                        {" "}
                        (
                        {mo.days
                          .map((d) => `${d.day.slice(8)}: ${formatCount(d.value)}`)
                          .join(", ")}
                        )
                      </span>
                    ) : null}
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
