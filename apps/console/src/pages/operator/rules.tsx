import { type QueryClient, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { ChevronDown, Download, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { ApiError } from "@/api/client";
import {
  type CohortValue,
  cohortListQuery,
  type ListedRule,
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
import { NativeSelect } from "@/components/form-field";
import { PageHeader } from "@/components/page-header";
import { DateField, isIsoDay } from "@/components/range-control";
import { RowMenu } from "@/components/row-menu";
import { EmptyState, ErrorState, LoadingState, Skeleton } from "@/components/states";
import { toast } from "@/components/toast";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
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

type RulePart = { text: string; value?: boolean };

// the rule as one sentence whose values are marked: who, what, how much,
// how often, and the grade
export function ruleParts(
  r: Pick<Rule, "metric" | "min_per_day" | "min_days" | "months" | "min_grade" | "cohort">,
): RulePart[] {
  const who = cohortText(r.cohort);
  const parts: RulePart[] = [
    { text: who ?? "Every project", value: true },
    { text: who ? "projects with" : "with" },
    { text: METRIC_TEXT[r.metric].toLowerCase().replace("api", "API"), value: true },
    { text: "of at least" },
    { text: formatCount(r.min_per_day), value: true },
    { text: "a day on" },
    { text: String(r.min_days), value: true },
    { text: "or more days in" },
    {
      text:
        r.months === 1 ? "the last complete month" : `each of the last ${r.months} complete months`,
      value: true,
    },
  ];
  if (r.min_grade)
    parts.push({
      text: r.min_grade === "A" ? "grade A" : `grade ${r.min_grade} or better`,
      value: true,
    });
  return parts;
}

function RuleSentence({
  rule,
  size = "lg",
}: {
  rule: Parameters<typeof ruleParts>[0];
  size?: "lg" | "sm";
}) {
  const parts = ruleParts(rule);
  return (
    <p
      className={cn(
        "break-words text-fg-2",
        size === "lg" ? "text-[17px] leading-[30px]" : "text-small leading-6",
      )}
    >
      {parts.map((p, i) => (
        <span key={i}>
          {i > 0 ? (parts[i - 1]?.value && p.value ? ", " : " ") : null}
          {p.value ? (
            size === "lg" ? (
              <span className="rounded-md border border-line-2 bg-sunken px-2 py-px font-medium text-fg [box-decoration-break:clone]">
                {p.text}
              </span>
            ) : (
              <b className="font-semibold text-fg">{p.text}</b>
            )
          ) : (
            p.text
          )}
        </span>
      ))}
    </p>
  );
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

// the form read back as a rule, for the sentence under it; a field that is
// not a number yet reads as its text
function formRule(f: RuleForm): Parameters<typeof ruleParts>[0] {
  const n = (v: string) => (/^\d+$/.test(v.trim()) ? Number(v.trim()) : 0);
  const opt = (v: string) => (v.trim() === "" ? null : v.trim());
  return {
    metric: f.metric,
    min_per_day: n(f.minPerDay),
    min_days: n(f.minDays),
    months: Math.max(1, n(f.months)),
    min_grade: f.minGrade === "" ? null : f.minGrade,
    cohort: { program: opt(f.program), round: opt(f.round), category: opt(f.category) },
  };
}

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
  const qc = useQueryClient();
  const [editing, setEditing] = useState<{ id: string | null; form: RuleForm } | null>(null);
  async function remove(r: Rule) {
    await ruleApi.remove(r.id);
    if (r.id === selected) setParams({}, { replace: true });
    toast.success(`${r.name} is deleted.`);
    await refreshRules(qc);
  }
  const open = (id: string) => setParams({ rule: id }, { replace: true });

  return (
    <div className="flex min-w-0 flex-col gap-6">
      <PageHeader
        title="Rules"
        count={rules.data ? items.length : null}
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
        <EmptyState title="No rule is saved yet" />
      ) : (
        <ul aria-label="Rules" className="flex min-w-0 flex-col gap-4">
          {items.map((r) => (
            <li key={r.id}>
              <RuleCard
                rule={r}
                open={r.id === selected}
                onToggle={() => (r.id === selected ? setParams({}, { replace: true }) : open(r.id))}
                onEdit={() => setEditing({ id: r.id, form: formOf(r) })}
                onDelete={() => remove(r)}
                asOf={r.id === selected ? asOf : ""}
                onAsOf={(d) =>
                  setParams(d ? { rule: r.id, as_of: d } : { rule: r.id }, { replace: true })
                }
              />
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

// one rule: its name, the rule as a sentence, how many meet it with a bar,
// and the way to its matches, its edit and its delete. open, it shows the
// evaluation date, the matches CSV and the projects that meet it.
function RuleCard({
  rule: r,
  open,
  onToggle,
  onEdit,
  onDelete,
  asOf,
  onAsOf,
}: {
  rule: ListedRule;
  open: boolean;
  onToggle: () => void;
  onEdit: () => void;
  onDelete: () => Promise<void>;
  asOf: string;
  onAsOf: (d: string) => void;
}) {
  const share = r.evaluated > 0 ? Math.round((r.match_count / r.evaluated) * 100) : 0;
  const bodyId = `rule-${r.id}-matches`;
  return (
    <article
      aria-labelledby={`rule-${r.id}-h`}
      data-rule={r.id}
      data-current={open ? "" : undefined}
      className="overflow-hidden rounded-2xl border border-line bg-raised"
    >
      <div className="grid min-w-0 gap-x-6 gap-y-3 px-5 py-4 sm:px-6 md:grid-cols-[minmax(0,1fr)_14rem_auto] md:items-center">
        <div className="flex min-w-0 flex-col gap-1">
          <h2 id={`rule-${r.id}-h`} className="text-body font-semibold text-fg">
            {r.name}
          </h2>
          <RuleSentence rule={r} />
        </div>
        <div className="flex min-w-0 flex-col gap-2" data-matches={r.id}>
          <p className="flex items-baseline gap-1.5 text-small text-fg-3">
            <span className="text-[19px] font-semibold text-fg tabular">
              {formatCount(r.match_count)}
            </span>{" "}
            of {formatCount(r.evaluated)} meet it
          </p>
          <span aria-hidden className="flex h-1.5 w-full overflow-hidden rounded-full bg-selected">
            <i className="block h-full rounded-full bg-fg-3" style={{ width: `${share}%` }} />
          </span>
        </div>
        <div className="flex items-center gap-1 justify-self-end max-md:hidden">
          <Button
            variant="ghost"
            size="icon-sm"
            aria-expanded={open}
            aria-controls={bodyId}
            aria-label={
              open
                ? `Hide the projects that meet ${r.name}`
                : `Show the projects that meet ${r.name}`
            }
            onClick={onToggle}
          >
            <ChevronDown
              aria-hidden
              className={cn(
                "transition-transform duration-(--vs-duration-change) ease-standard",
                open && "rotate-180",
              )}
            />
          </Button>
          <RuleMenu rule={r} onEdit={onEdit} onDelete={onDelete} />
        </div>
        <div className="flex items-center gap-2 md:hidden">
          <Button
            variant="outline"
            size="sm"
            aria-expanded={open}
            aria-controls={bodyId}
            onClick={onToggle}
            className="h-(--vs-control-touch) flex-1"
          >
            {open ? "Hide the projects" : "Show the projects"}
          </Button>
          <RuleMenu rule={r} onEdit={onEdit} onDelete={onDelete} />
        </div>
      </div>
      {open ? (
        <div id={bodyId} className="animate-fade-in border-t border-line">
          <Matches rule={r} asOf={asOf} onAsOf={onAsOf} />
        </div>
      ) : (
        <span id={bodyId} hidden />
      )}
    </article>
  );
}

function RuleMenu({
  rule: r,
  onEdit,
  onDelete,
}: {
  rule: ListedRule;
  onEdit: () => void;
  onDelete: () => Promise<void>;
}) {
  return (
    <RowMenu
      name={r.name}
      actions={[
        { label: "Edit rule", onSelect: onEdit },
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
            run: onDelete,
          },
        },
      ]}
    />
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
}: {
  field: "program" | "round" | "category";
  values: CohortValue[];
  loading: boolean;
  value: string;
  onChange: (e: React.ChangeEvent<HTMLSelectElement>) => void;
}) {
  const opts = values.map((v) => v.value);
  // a rule saved with a value no project holds any more keeps showing it
  if (value && !opts.includes(value)) opts.unshift(value);
  return (
    <NativeSelect
      aria-label={COHORT_LABEL[field]}
      value={value}
      onChange={onChange}
      disabled={loading}
      className="w-fit max-w-full"
    >
      <option value="">{loading ? "Loading" : ANY[field]}</option>
      {opts.map((o) => (
        <option key={o} value={o}>
          {o}
        </option>
      ))}
    </NativeSelect>
  );
}

const ANY = { program: "Any program", round: "Any round", category: "Any category" } as const;

// one part of the rule: its name at the left, its fields as a phrase, and
// the errors of the part under them
function FormRow({
  label,
  children,
  errors,
}: {
  label: string;
  children: React.ReactNode;
  errors?: (string | undefined)[];
}) {
  const shown = (errors ?? []).filter((e): e is string => Boolean(e));
  return (
    <fieldset className="grid min-w-0 gap-x-4 gap-y-1.5 border-b border-line py-3 first:pt-0 sm:grid-cols-[6.5rem_minmax(0,1fr)] sm:items-center">
      <legend className="sr-only">{label}</legend>
      <span aria-hidden className="text-small text-fg-2">
        {label}
      </span>
      <div className="flex min-w-0 flex-wrap items-center gap-2 text-small text-fg-2">
        {children}
      </div>
      {shown.length ? (
        <p role="alert" className="text-small text-critical sm:col-start-2">
          {shown.join(" ")}
        </p>
      ) : null}
    </fieldset>
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
      <div className="flex min-w-0 flex-col">
        <FormRow
          label="Who"
          errors={[
            errors.program,
            errors.round,
            errors.category,
            cohorts.error ? "The cohort values did not load. Reload the page." : undefined,
          ]}
        >
          {(["program", "round", "category"] as const).map((k) => (
            <CohortSelect
              key={k}
              field={k}
              values={cohorts.data?.[COHORT_LIST[k]] ?? []}
              loading={cohorts.isPending}
              value={form[k]}
              onChange={set(k)}
            />
          ))}
        </FormRow>
        <FormRow label="Counts" errors={[errors.metric, errors.minPerDay]}>
          <NativeSelect
            aria-label="Metric"
            value={form.metric}
            onChange={set("metric")}
            className="w-fit"
          >
            {(Object.keys(METRIC_TEXT) as RuleMetric[]).map((m) => (
              <option key={m} value={m}>
                {METRIC_TEXT[m]}
              </option>
            ))}
          </NativeSelect>
          <span>at least</span>
          <Input
            aria-label="Minimum per day"
            inputMode="numeric"
            value={form.minPerDay}
            onChange={set("minPerDay")}
            aria-invalid={errors.minPerDay ? true : undefined}
            className="w-24 tabular"
          />
          <span>a day</span>
        </FormRow>
        <FormRow label="How often" errors={[errors.minDays, errors.months]}>
          <Input
            aria-label="Days per month, 1 to 28"
            inputMode="numeric"
            value={form.minDays}
            onChange={set("minDays")}
            aria-invalid={errors.minDays ? true : undefined}
            className="w-[4.75rem] tabular"
          />
          <span>or more days in each of the last</span>
          <Input
            aria-label="Complete months, 1 to 12"
            inputMode="numeric"
            value={form.months}
            onChange={set("months")}
            aria-invalid={errors.months ? true : undefined}
            className="w-16 tabular"
          />
          <span>complete months</span>
        </FormRow>
        <FormRow label="Grade" errors={[errors.minGrade]}>
          <NativeSelect
            aria-label="Integrity grade"
            value={form.minGrade}
            onChange={set("minGrade")}
            className="w-fit"
          >
            <option value="">Any grade</option>
            <option value="A">A</option>
            <option value="B">B or better</option>
            <option value="C">C or better</option>
          </NativeSelect>
        </FormRow>
        <FormRow label="Name" errors={[errors.name]}>
          <Input
            aria-label="Name"
            value={form.name}
            onChange={set("name")}
            maxLength={100}
            aria-invalid={errors.name ? true : undefined}
            className="min-w-0 flex-1"
          />
        </FormRow>
      </div>
      <div className="mt-1 rounded-xl border border-line bg-sunken px-4 py-3" aria-live="polite">
        <RuleSentence size="sm" rule={formRule(form)} />
      </div>
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
    <section aria-label={`Projects that meet ${rule.name}`} className="flex min-w-0 flex-col">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-line px-5 py-2.5 sm:px-6">
        <div className="w-44 [&_label]:sr-only">
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
        <span aria-hidden className="order-first text-small text-fg-3">
          Evaluated on
        </span>
        {q.data ? (
          <span
            className="text-small text-fg-3"
            title={`${formatDateRange(q.data.from, q.data.to)}, UTC`}
          >
            {monthsText(q.data.months)}, UTC
          </span>
        ) : null}
        <a
          href={ruleMatchesCsv(rule.id, asOf)}
          download
          className="ml-auto inline-flex items-center gap-1.5 rounded-sm text-small text-fg-2 hover:text-fg focus-ring max-lg:min-h-(--vs-control-touch)"
        >
          <Download aria-hidden className="size-3.5" />
          Matches CSV
        </a>
      </div>
      {q.isPending ? (
        <div className="p-5">
          <LoadingState label="Evaluating the rule">
            <Skeleton.Table rows={3} columns={4} />
          </LoadingState>
        </div>
      ) : q.error ? (
        <div className="p-5">
          <ErrorState
            title="The rule did not evaluate"
            error={q.error}
            onRetry={() => q.refetch()}
          />
        </div>
      ) : (
        <MatchList data={q.data} />
      )}
    </section>
  );
}

const monthFmt = new Intl.DateTimeFormat("en-US", {
  month: "long",
  year: "numeric",
  timeZone: "UTC",
});

// "October 2026", or "August 2026 to October 2026"
function monthsText(months: string[]): string {
  const name = (m: string) => monthFmt.format(new Date(`${m}-01T00:00:00Z`));
  const first = months[0];
  const last = months.at(-1);
  if (!first || !last) return "";
  return first === last ? name(first) : `${name(first)} to ${name(last)}`;
}

function daysIn(month: string): number {
  const [y, m] = month.split("-").map(Number);
  return new Date(Date.UTC(y ?? 1970, m ?? 1, 0)).getUTCDate();
}

// the projects that meet the rule, with their cohort and their qualifying
// days; each month's days and values are on the hover of its count
function MatchList({ data }: { data: RuleMatches }) {
  if (data.items.length === 0)
    return (
      <p className="px-5 py-6 text-small text-fg-3 sm:px-6" aria-live="polite">
        No project of {countOf(data.evaluated, "project")} meets the rule.
      </p>
    );
  return (
    <Table aria-label="Projects that meet the rule">
      <TableHeader>
        <TableRow>
          <TableHead>Project</TableHead>
          <TableHead className="max-md:hidden">Program</TableHead>
          <TableHead className="max-md:hidden">Round</TableHead>
          <TableHead className="max-md:hidden">Category</TableHead>
          <TableHead className="max-md:hidden">Grade</TableHead>
          <TableHead className="text-right">Qualifying days</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {data.items.map((m) => (
          <TableRow key={m.tenant.id} data-tenant={m.tenant.id}>
            <TableCell>
              <Link
                to={`/operator/projects/${m.tenant.id}`}
                className="rounded-sm font-medium text-fg underline-offset-4 hover:underline focus-ring"
              >
                {m.tenant.name}
              </Link>
            </TableCell>
            <TableCell className="text-fg-2 max-md:hidden">{m.cohort.program ?? ""}</TableCell>
            <TableCell className="text-fg-2 max-md:hidden">{m.cohort.round ?? ""}</TableCell>
            <TableCell className="text-fg-2 max-md:hidden">{m.cohort.category ?? ""}</TableCell>
            <TableCell
              className="text-fg-2 max-md:hidden"
              title={`${countOf(m.integrity.graded_days, "graded site day")}`}
            >
              {m.integrity.grade ?? "None"}
            </TableCell>
            <TableCell className="text-right tabular">
              <ul className="flex flex-col items-end">
                {m.months.map((mo) => (
                  <li
                    key={mo.month}
                    data-month={mo.month}
                    title={mo.days
                      .map((d) => `${formatDate(d.day)}: ${formatCount(d.value)}`)
                      .join("\n")}
                  >
                    {data.months.length > 1 ? (
                      <span className="mr-2 text-fg-3">{mo.month}</span>
                    ) : null}
                    {formatCount(mo.qualifying_days)} of {daysIn(mo.month)}
                  </li>
                ))}
              </ul>
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}
