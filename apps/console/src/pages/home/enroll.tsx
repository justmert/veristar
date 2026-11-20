import { useMutation, useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { Check } from "lucide-react";
import { type FormEvent, type ReactNode, useEffect, useId, useRef, useState } from "react";
import { Link, useNavigate } from "react-router";
import { ApiError } from "@/api/client";
import { api, type EnrollmentInput } from "@/api/endpoints";
import { keys, meQuery } from "@/api/queries";
import type { ScfAward } from "@/api/schemas";
import {
  CohortFields,
  type CohortValues,
  cohortBody,
  EMPTY_COHORT,
  isEmptyCohort,
  validateCohort,
} from "@/components/cohort";
import { Disclosure } from "@/components/disclosure";
import { FormMessage } from "@/components/error-panel";
import { TextAreaField, TextField } from "@/components/form-field";
import { LabeledFacts } from "@/components/labeled-facts";
import { PageHeader } from "@/components/page-header";
import { ErrorState } from "@/components/states";
import { toast } from "@/components/toast";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Skeleton } from "@/components/ui/skeleton";
import { formatDate } from "@/lib/format";
import { isDraftTerms, termsVersionLabel } from "@/lib/terms";
import * as v from "@/lib/validate";
import { queryClient } from "@/query-client";
import { AwardSearch, awardCohort } from "./award-search";

export type EnrollValues = {
  name: string;
  slug: string;
  description: string;
  website: string;
  stellarUsage: string;
  stellarIds: string;
  scfRoundUrl: string;
  termsAccepted: boolean;
};

export const emptyEnrollValues: EnrollValues = {
  name: "",
  slug: "",
  description: "",
  website: "",
  stellarUsage: "",
  stellarIds: "",
  scfRoundUrl: "",
  termsAccepted: false,
};

type FieldKey = keyof EnrollValues;
export type EnrollErrors = Partial<Record<FieldKey, string>>;

// one id per line or separated by commas or spaces, as people paste them
export function splitIds(text: string): string[] {
  return text
    .split(/[\s,]+/)
    .map((s) => s.trim())
    .filter((s) => s !== "");
}

export function validateEnrollment(values: EnrollValues): EnrollErrors {
  const errors: EnrollErrors = {};
  const put = (k: FieldKey, code: string | null) => {
    if (code && !errors[k]) errors[k] = v.fieldMessage(code);
  };
  put("name", v.tenantName(values.name));
  put("slug", v.slug(values.slug.trim()));
  put("description", v.description(values.description));
  put("website", v.httpsUrl(values.website.trim(), true));
  put("stellarUsage", v.stellarUsage(values.stellarUsage));
  put("scfRoundUrl", v.httpsUrl(values.scfRoundUrl.trim(), false));
  const ids = splitIds(values.stellarIds);
  if (ids.length > 20) errors.stellarIds = "At most 20 IDs.";
  else {
    const bad = ids.findIndex((id) => v.strkey(id) !== null);
    if (bad >= 0) errors.stellarIds = `${ids[bad]}: ${v.fieldMessage("invalid_strkey")}`;
    else if (new Set(ids).size !== ids.length) errors.stellarIds = v.fieldMessage("duplicate");
  }
  if (!values.termsAccepted) errors.termsAccepted = "Accept the terms to enroll.";
  return errors;
}

export function enrollmentBody(values: EnrollValues, termsVersion: string): EnrollmentInput {
  const ids = splitIds(values.stellarIds);
  const slug = values.slug.trim();
  const description = values.description.trim();
  const scf = values.scfRoundUrl.trim();
  return {
    name: values.name.trim(),
    ...(slug ? { slug } : {}),
    ...(description ? { description } : {}),
    enrollment: {
      website: values.website.trim(),
      stellar_usage: values.stellarUsage.trim(),
      ...(ids.length ? { stellar_ids: ids } : {}),
      ...(scf ? { scf_round_url: scf } : {}),
    },
    accepted_terms_version: termsVersion,
  };
}

// maps the json paths of a 422 (contract 2.2) onto the form's fields
const SERVER_PATHS: Record<string, FieldKey> = {
  name: "name",
  slug: "slug",
  description: "description",
  "enrollment.website": "website",
  "enrollment.stellar_usage": "stellarUsage",
  "enrollment.stellar_ids": "stellarIds",
  "enrollment.scf_round_url": "scfRoundUrl",
  accepted_terms_version: "termsAccepted",
};

// the optional cohort proposal of contract 19.1: sent as the top-level
// cohort object, left out when every field is empty
export function withCohort(body: EnrollmentInput, cohort: CohortValues): EnrollmentInput {
  if (isEmptyCohort(cohort)) return body;
  const c = cohortBody(cohort);
  const kept = Object.fromEntries(Object.entries(c).filter(([, v]) => v !== null));
  return { ...body, cohort: kept };
}

// the award picked with the search and the cohort it filled in. the form
// names the award only while that cohort is unchanged; an edited cohort is
// sent as typed (contract 4.2.1: never both)
export type PickedAward = { id: string; project: string; cohort: CohortValues };

export function sameCohort(a: CohortValues, b: CohortValues): boolean {
  return (["program", "round", "category", "award_date"] as const).every(
    (k) => a[k].trim() === b[k].trim(),
  );
}

export function withAward(
  body: EnrollmentInput,
  cohort: CohortValues,
  award: PickedAward | null,
): EnrollmentInput {
  if (award && sameCohort(cohort, award.cohort)) return { ...body, scf_award_id: award.id };
  return withCohort(body, cohort);
}

// the stellar ids of an award in the form's field: one per line, at most 20
export function awardIds(ids: string[]): string {
  return ids.slice(0, 20).join("\n");
}

// maps the cohort paths of a 422 onto the cohort fields
export function serverCohortErrors(err: unknown): Partial<Record<keyof CohortValues, string>> {
  if (!(err instanceof ApiError) || err.code !== "validation_failed") return {};
  const out: Partial<Record<keyof CohortValues, string>> = {};
  for (const [path, code] of Object.entries(err.fields)) {
    const m = /^cohort\.(program|round|category|award_date)$/.exec(path);
    if (m?.[1]) out[m[1] as keyof CohortValues] = v.fieldMessage(code);
  }
  return out;
}

export function serverEnrollErrors(err: unknown, values: EnrollValues): EnrollErrors {
  if (!(err instanceof ApiError)) return {};
  if (err.code === "slug_taken") return { slug: "This short name is taken. Choose another one." };
  if (err.code === "terms_version_mismatch")
    return { termsAccepted: "The terms changed. Read the new version and accept it again." };
  if (err.code !== "validation_failed") return {};
  const out: EnrollErrors = {};
  const ids = splitIds(values.stellarIds);
  for (const [path, code] of Object.entries(err.fields)) {
    const idx = /^enrollment\.stellar_ids\[(\d+)\]$/.exec(path);
    if (idx?.[1] !== undefined) {
      const id = ids[Number(idx[1])];
      out.stellarIds = `${id ?? `Entry ${Number(idx[1]) + 1}`}: ${v.fieldMessage(code)}`;
      continue;
    }
    const key = SERVER_PATHS[path];
    if (key && !out[key]) out[key] = v.fieldMessage(code);
  }
  return out;
}

// the fields under "More about the project"; a refused submit opens the
// section when one of them needs attention
const MORE: readonly FieldKey[] = ["slug", "description", "scfRoundUrl"];

const CRUMB = (
  <>
    <Link
      to="/"
      className="rounded-sm transition-colors duration-(--vs-duration-press) hover:text-fg focus-ring"
    >
      My projects
    </Link>
    <span aria-hidden>/</span>
  </>
);

const HEADER = { title: "Enroll a project", crumb: CRUMB };

export function EnrollPage() {
  const me = useQuery(meQuery);
  if (me.isPending)
    return (
      <section className="flex flex-col gap-6">
        <PageHeader {...HEADER} />
        <div role="status" aria-busy="true" className="flex max-w-prose flex-col gap-4">
          <span className="sr-only">Loading the form</span>
          <Skeleton className="h-9 w-full" />
          <Skeleton className="h-9 w-full" />
          <Skeleton className="h-24 w-full" />
        </div>
      </section>
    );
  if (me.error)
    return (
      <section className="flex flex-col gap-6">
        <PageHeader {...HEADER} />
        <ErrorState
          title="Your account did not load"
          error={me.error}
          onRetry={() => me.refetch()}
        />
      </section>
    );
  return <EnrollForm termsVersion={me.data.terms.current_version} />;
}

// one numbered section of the form: its mark (the number, or a check once
// it is complete) beside the heading and the fields
export function NumberedSection({
  n,
  done = false,
  title,
  children,
}: {
  n: number;
  done?: boolean;
  title: string;
  children: ReactNode;
}) {
  const id = useId();
  return (
    <section
      aria-labelledby={id}
      data-section={n}
      className="grid grid-cols-[26px_minmax(0,1fr)] gap-x-4 border-b border-line-subtle pb-5 last:border-b-0"
    >
      <span
        aria-hidden
        className={cn(
          "mt-0.5 flex size-6.5 items-center justify-center rounded-full border text-caption tabular",
          done ? "border-success-line text-success" : "border-line-strong text-fg-2",
        )}
      >
        {done ? <Check className="size-3.5" strokeWidth={2.4} /> : n}
      </span>
      <div className="flex min-w-0 flex-col gap-4">
        <h2 id={id} className="pt-1 text-[14.5px] leading-5 font-semibold text-fg">
          {title}
          {done ? <span className="sr-only">, complete</span> : null}
        </h2>
        {children}
      </div>
    </section>
  );
}

// an optional group that starts closed; `open` forces it open
export function Optional({
  title,
  hint,
  open,
  onToggle,
  nested,
  children,
}: {
  title: string;
  hint: string;
  open: boolean;
  onToggle: (open: boolean) => void;
  // inside a numbered step: the chevron lines up with the step's fields,
  // not with the step numbers
  nested?: boolean;
  children: ReactNode;
}) {
  return (
    <Disclosure
      open={open}
      onToggle={onToggle}
      variant="row"
      className="bg-transparent"
      summaryClassName={cn(
        "min-h-12 text-[14.5px] font-normal",
        nested ? "gap-2" : "gap-4.5 pl-1.25",
      )}
      bodyClassName={cn("flex flex-col gap-4 pt-1 pb-5", nested ? "pl-6" : "pl-10.5")}
      summary={
        <span className="flex items-baseline gap-2">
          <span className="font-semibold text-fg">{title}</span>
          <span className="text-small text-fg-3">{hint}</span>
        </span>
      }
    >
      {children}
    </Disclosure>
  );
}

export function EnrollForm({ termsVersion }: { termsVersion: string }) {
  const navigate = useNavigate();
  const [values, setValues] = useState<EnrollValues>(emptyEnrollValues);
  const [errors, setErrors] = useState<EnrollErrors>({});
  const [cohort, setCohort] = useState<CohortValues>(EMPTY_COHORT);
  const [cohortErrors, setCohortErrors] = useState<Partial<Record<keyof CohortValues, string>>>({});
  const [moreOpen, setMoreOpen] = useState(false);
  const [cohortOpen, setCohortOpen] = useState(false);
  const [award, setAward] = useState<PickedAward | null>(null);
  const [awardError, setAwardError] = useState<string | null>(null);

  function pick(a: ScfAward) {
    const c = awardCohort(a);
    setValues((s) => ({
      ...s,
      name: a.project,
      website: a.website ?? s.website,
      stellarIds: a.stellar_ids.length ? awardIds(a.stellar_ids) : s.stellarIds,
    }));
    setCohort(c);
    setAward({ id: a.id, project: a.project, cohort: c });
    setAwardError(null);
  }
  const termsId = useId();
  const formRef = useRef<HTMLFormElement>(null);
  const [focusTick, setFocusTick] = useState(0);

  // after a refused submit, move focus to the first field that needs
  // attention; it runs after the render that opened its section
  useEffect(() => {
    if (focusTick === 0) return;
    formRef.current?.querySelector<HTMLElement>("[aria-invalid=true]")?.focus();
  }, [focusTick]);

  function show(found: EnrollErrors, cohortFound: Partial<Record<keyof CohortValues, string>>) {
    setErrors(found);
    setCohortErrors(cohortFound);
    if (MORE.some((k) => found[k])) setMoreOpen(true);
    if (Object.keys(cohortFound).length > 0) setCohortOpen(true);
  }

  const enroll = useMutation({
    mutationFn: (body: EnrollmentInput) => api.createTenant(body),
    onSuccess: async (tenant) => {
      queryClient.setQueryData(keys.tenant(tenant.id), tenant);
      await queryClient.invalidateQueries({ queryKey: keys.me });
      toast.success(`${tenant.name} is enrolled. Add its first site.`);
      navigate(`/projects/${tenant.id}/sites`, { state: { enrolled: true } });
    },
    onError: async (err) => {
      show(serverEnrollErrors(err, values), serverCohortErrors(err));
      // an award the directory no longer holds: the cohort is sent as typed
      // next time, and the search says what to do
      if (err instanceof ApiError && err.fields.scf_award_id) {
        setAward(null);
        setAwardError(v.fieldMessage(err.fields.scf_award_id));
      }
      setFocusTick((n) => n + 1);
      if (err instanceof ApiError && err.code === "terms_version_mismatch") {
        setValues((s) => ({ ...s, termsAccepted: false }));
        await queryClient.invalidateQueries({ queryKey: keys.me });
        await queryClient.invalidateQueries({ queryKey: keys.terms });
      }
    },
  });

  const set = (k: FieldKey) => (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) =>
    setValues((s) => ({ ...s, [k]: e.target.value }));

  function submit(e: FormEvent) {
    e.preventDefault();
    const found = validateEnrollment(values);
    const cohortFound = validateCohort(cohort, new Date().toISOString().slice(0, 10));
    show(found, cohortFound);
    if (Object.keys(found).length > 0 || Object.keys(cohortFound).length > 0) {
      setFocusTick((n) => n + 1);
      return;
    }
    enroll.mutate(withAward(enrollmentBody(values, termsVersion), cohort, award));
  }

  const otherError =
    enroll.error &&
    Object.keys(serverEnrollErrors(enroll.error, values)).length === 0 &&
    Object.keys(serverCohortErrors(enroll.error)).length === 0
      ? enroll.error
      : null;

  const errorCount = Object.keys(errors).length + Object.keys(cohortErrors).length;

  const pickedFacts = award ? awardFacts(award.cohort) : [];

  return (
    <section className="flex flex-col gap-6">
      <PageHeader {...HEADER} />
      <form
        ref={formRef}
        noValidate
        onSubmit={submit}
        className="flex max-w-180 flex-col gap-5"
        aria-label="Enroll a project"
      >
        <NumberedSection n={1} done={award !== null} title="SCF award">
          {award ? (
            <div
              data-award-picked={award.id}
              className="flex min-w-0 items-center gap-3 rounded-xl border border-line bg-raised px-4 py-3.5 sm:pl-10.5 sm:relative"
            >
              <Check
                aria-hidden
                className="size-4 shrink-0 text-success sm:absolute sm:top-1/2 sm:left-4 sm:-translate-y-1/2"
                strokeWidth={2.4}
              />
              <div className="flex min-w-0 flex-1 flex-col gap-2">
                <span className="truncate text-[14.5px] font-medium text-fg">{award.project}</span>
                <LabeledFacts items={pickedFacts} />
              </div>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                className="shrink-0 max-sm:h-(--vs-control-touch)"
                onClick={() => {
                  setAward(null);
                  setCohort(EMPTY_COHORT);
                }}
              >
                Change
                <span className="sr-only"> the SCF award</span>
              </Button>
            </div>
          ) : (
            <>
              <div className="flex flex-col gap-1">
                <AwardSearch onPick={pick} />
                {awardError ? (
                  <p role="alert" className="text-caption text-critical" data-award-error="">
                    {awardError}
                  </p>
                ) : null}
              </div>
              <Optional
                title="Enter the award by hand"
                hint="optional"
                nested
                open={cohortOpen}
                onToggle={setCohortOpen}
              >
                <CohortFields
                  values={cohort}
                  onChange={setCohort}
                  errors={cohortErrors}
                  idPrefix="enroll-cohort"
                />
              </Optional>
            </>
          )}
        </NumberedSection>

        <NumberedSection n={2} title="Project">
          <TextField
            label="Name"
            name="name"
            autoComplete="organization"
            maxLength={100}
            value={values.name}
            onChange={set("name")}
            error={errors.name}
            required
          />
          <TextField
            label="Website"
            name="website"
            type="url"
            inputMode="url"
            placeholder="https://"
            className="font-mono"
            autoCapitalize="none"
            spellCheck={false}
            value={values.website}
            onChange={set("website")}
            error={errors.website}
            required
          />
        </NumberedSection>

        <NumberedSection n={3} title="Stellar">
          <StellarFields
            usage={values.stellarUsage}
            ids={values.stellarIds}
            onUsage={set("stellarUsage")}
            onIds={set("stellarIds")}
            usageError={errors.stellarUsage}
            idsError={errors.stellarIds}
          />
        </NumberedSection>

        <div className="border-b border-line-subtle">
          <Optional
            title="More about the project"
            hint="optional"
            open={moreOpen}
            onToggle={setMoreOpen}
          >
            <TextField
              label="Short name"
              name="slug"
              optional
              placeholder="lowercase-letters-and-digits"
              autoCapitalize="none"
              spellCheck={false}
              className="font-mono"
              maxLength={63}
              value={values.slug}
              onChange={set("slug")}
              error={errors.slug}
            />
            <TextAreaField
              label="Description"
              name="description"
              optional
              rows={2}
              maxLength={1000}
              value={values.description}
              onChange={set("description")}
              error={errors.description}
            />
            <TextField
              label="SCF submission link"
              name="scf_round_url"
              optional
              type="url"
              inputMode="url"
              placeholder="https://"
              autoCapitalize="none"
              spellCheck={false}
              value={values.scfRoundUrl}
              onChange={set("scfRoundUrl")}
              error={errors.scfRoundUrl}
            />
          </Optional>
        </div>

        <div className="flex flex-col gap-5 sm:pl-10.5">
          <div className="flex flex-col gap-2">
            <div className="flex items-start gap-3">
              <Checkbox
                id={termsId}
                checked={values.termsAccepted}
                onCheckedChange={(c) => setValues((s) => ({ ...s, termsAccepted: c === true }))}
                aria-invalid={errors.termsAccepted ? true : undefined}
                aria-describedby={errors.termsAccepted ? `${termsId}-e` : undefined}
                className="mt-0.75"
              />
              <label
                htmlFor={termsId}
                className="flex flex-wrap items-baseline gap-x-2 gap-y-1 text-body text-fg-2"
              >
                <span>
                  I accept the{" "}
                  <a
                    href="/terms"
                    target="_blank"
                    rel="noopener"
                    className="rounded-sm text-fg underline decoration-line-strong underline-offset-3 transition-colors duration-(--vs-duration-press) hover:decoration-fg focus-ring"
                  >
                    terms of the service
                  </a>{" "}
                  for this project
                </span>
                <span className="text-caption text-fg-3" title={termsVersion}>
                  {termsVersionLabel(termsVersion)}
                </span>
                {isDraftTerms(termsVersion) ? (
                  <span className="text-caption text-fg-3">Draft</span>
                ) : null}
              </label>
            </div>
            {errors.termsAccepted ? (
              <p
                id={`${termsId}-e`}
                role="alert"
                className="animate-fade-in text-caption text-critical"
              >
                {errors.termsAccepted}
              </p>
            ) : null}
          </div>

          <div className="flex flex-wrap items-center gap-3">
            <Button
              type="submit"
              pending={enroll.isPending}
              className="h-10 px-5 max-sm:h-(--vs-control-touch) max-sm:flex-1"
            >
              {enroll.isPending ? "Enrolling" : "Enroll project"}
            </Button>
            <Button asChild variant="ghost" className="max-sm:h-(--vs-control-touch)">
              <Link to="/">Cancel</Link>
            </Button>
            {errorCount > 0 ? (
              <span role="status" className="max-sm:basis-full">
                <button
                  type="button"
                  onClick={() => setFocusTick((n) => n + 1)}
                  className="rounded-sm text-left text-caption text-critical underline decoration-critical-line underline-offset-2 hover:decoration-critical focus-ring max-sm:py-2"
                >
                  {errorCount === 1
                    ? "1 field needs attention. Go to it."
                    : `${errorCount} fields need attention. Go to the first.`}
                </button>
              </span>
            ) : null}
            {otherError ? <FormMessage error={otherError} className="w-full" /> : null}
          </div>
        </div>
      </form>
    </section>
  );
}

// the cohort of a picked award as labeled facts; an empty field is left out
export function awardFacts(c: CohortValues) {
  return [
    { label: "Program", value: c.program },
    { label: "Round", value: c.round },
    { label: "Category", value: c.category },
    { label: "Awarded", value: c.award_date ? formatDate(c.award_date) : "" },
  ].filter((f) => f.value.trim() !== "");
}

// how the project uses stellar, with its counter, and its optional ids:
// the same two fields on enroll and on resubmit
export function StellarFields({
  usage,
  ids,
  onUsage,
  onIds,
  usageError,
  idsError,
}: {
  usage: string;
  ids: string;
  onUsage: (e: React.ChangeEvent<HTMLTextAreaElement>) => void;
  onIds: (e: React.ChangeEvent<HTMLTextAreaElement>) => void;
  usageError?: string;
  idsError?: string;
}) {
  return (
    <>
      <TextAreaField
        label="How the project uses Stellar"
        name="stellar_usage"
        rows={3}
        maxLength={2000}
        hint={<span className="flex justify-end tabular text-fg-2">{usage.length} / 2000</span>}
        value={usage}
        onChange={onUsage}
        error={usageError}
        required
      />
      <TextAreaField
        label="Account or contract IDs"
        name="stellar_ids"
        optional
        rows={1}
        placeholder="One per line, G… M… or C…, up to 20"
        className="min-h-9.5 font-mono text-small"
        autoCapitalize="characters"
        spellCheck={false}
        value={ids}
        onChange={onIds}
        error={idsError}
      />
    </>
  );
}
