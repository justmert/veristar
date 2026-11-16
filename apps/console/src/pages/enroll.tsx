import { useMutation, useQuery } from "@tanstack/react-query";
import { type FormEvent, useEffect, useId, useRef, useState } from "react";
import { Link, useNavigate } from "react-router";
import { ApiError } from "@/api/client";
import { api, type EnrollmentInput } from "@/api/endpoints";
import { keys, meQuery } from "@/api/queries";
import {
  CohortFields,
  type CohortValues,
  cohortBody,
  EMPTY_COHORT,
  isEmptyCohort,
  validateCohort,
} from "@/components/cohort";
import { ErrorPanel, FormMessage, Loading } from "@/components/error-panel";
import { TextAreaField, TextField } from "@/components/form-field";
import { PageHeader } from "@/components/panel";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import * as v from "@/lib/validate";
import { queryClient } from "@/query-client";

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
  if (ids.length > 20) errors.stellarIds = "At most 20 ids.";
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
  if (err.code === "slug_taken") return { slug: "This address is taken. Choose another one." };
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

export function EnrollPage() {
  const me = useQuery(meQuery);
  if (me.isPending) return <Loading label="Loading" />;
  if (me.error) return <ErrorPanel error={me.error} onRetry={() => me.refetch()} />;
  return <EnrollForm termsVersion={me.data.terms.current_version} />;
}

export function EnrollForm({ termsVersion }: { termsVersion: string }) {
  const navigate = useNavigate();
  const [values, setValues] = useState<EnrollValues>(emptyEnrollValues);
  const [errors, setErrors] = useState<EnrollErrors>({});
  const [cohort, setCohort] = useState<CohortValues>(EMPTY_COHORT);
  const [cohortErrors, setCohortErrors] = useState<Partial<Record<keyof CohortValues, string>>>({});
  const termsId = useId();
  const formRef = useRef<HTMLFormElement>(null);
  const [focusTick, setFocusTick] = useState(0);

  // after a refused submit, move focus to the first field that needs
  // attention; it runs after the render that marked the fields invalid
  useEffect(() => {
    if (focusTick === 0) return;
    formRef.current?.querySelector<HTMLElement>("[aria-invalid=true]")?.focus();
  }, [focusTick]);

  const enroll = useMutation({
    mutationFn: (body: EnrollmentInput) => api.createTenant(body),
    onSuccess: async (tenant) => {
      queryClient.setQueryData(keys.tenant(tenant.id), tenant);
      await queryClient.invalidateQueries({ queryKey: keys.me });
      navigate(`/projects/${tenant.id}/sites`, { state: { enrolled: true } });
    },
    onError: async (err) => {
      setErrors(serverEnrollErrors(err, values));
      setCohortErrors(serverCohortErrors(err));
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
    setErrors(found);
    setCohortErrors(cohortFound);
    if (Object.keys(found).length > 0 || Object.keys(cohortFound).length > 0) {
      setFocusTick((n) => n + 1);
      return;
    }
    enroll.mutate(withCohort(enrollmentBody(values, termsVersion), cohort));
  }

  const otherError =
    enroll.error &&
    Object.keys(serverEnrollErrors(enroll.error, values)).length === 0 &&
    Object.keys(serverCohortErrors(enroll.error)).length === 0
      ? enroll.error
      : null;

  const errorCount = Object.keys(errors).length;
  const legend = "px-1.5 text-caption tracking-wide text-fg-3 uppercase";
  const fieldset =
    "flex min-w-0 flex-col gap-4 rounded-lg border border-line bg-raised px-4 pt-2 pb-5 sm:px-5";

  return (
    <section className="flex flex-col gap-6">
      <nav aria-label="Breadcrumb" className="flex items-center gap-2 text-small text-fg-2">
        <Link
          to="/"
          className="rounded-sm transition-colors duration-(--vs-duration-press) hover:text-fg focus-ring"
        >
          Your projects
        </Link>
        <span aria-hidden>/</span>
        <span className="text-fg">Enroll</span>
      </nav>
      <div className="grid items-start gap-10 xl:grid-cols-[minmax(0,42rem)_minmax(0,17rem)]">
        <div className="flex min-w-0 flex-col gap-6">
          <PageHeader
            title="Enroll a project"
            description="Tell us about the project. Operators of this service read these answers to keep it for the Stellar ecosystem."
          />

          <form
            ref={formRef}
            noValidate
            onSubmit={submit}
            className="flex flex-col gap-6"
            aria-label="Enroll a project"
          >
            <fieldset className={fieldset}>
              <legend className={legend}>The project</legend>
              <div className="grid gap-4 sm:grid-cols-2">
                <TextField
                  label="Project name"
                  name="name"
                  autoComplete="organization"
                  maxLength={100}
                  value={values.name}
                  onChange={set("name")}
                  error={errors.name}
                  required
                />
                <TextField
                  label="Project address"
                  name="slug"
                  optional
                  hint="Lowercase letters, digits and hyphens. Leave empty to derive it from the name."
                  autoCapitalize="none"
                  spellCheck={false}
                  className="font-mono"
                  maxLength={63}
                  value={values.slug}
                  onChange={set("slug")}
                  error={errors.slug}
                />
              </div>
              <TextField
                label="Website"
                name="website"
                type="url"
                inputMode="url"
                placeholder="https://"
                autoCapitalize="none"
                spellCheck={false}
                value={values.website}
                onChange={set("website")}
                error={errors.website}
                required
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
            </fieldset>

            <fieldset className={fieldset}>
              <legend className={legend}>Evidence for reviewers</legend>
              <TextAreaField
                label="How does the project use Stellar?"
                name="stellar_usage"
                rows={4}
                maxLength={2000}
                hint={
                  <span className="flex justify-between gap-4">
                    <span>
                      For example: a Soroban lending protocol on mainnet, or a wallet for Stellar
                      accounts.
                    </span>
                    <span className="shrink-0 font-mono tabular text-fg-3">
                      {values.stellarUsage.length} / 2000
                    </span>
                  </span>
                }
                value={values.stellarUsage}
                onChange={set("stellarUsage")}
                error={errors.stellarUsage}
                required
              />
              <TextAreaField
                label="Account or contract ids"
                name="stellar_ids"
                optional
                rows={3}
                hint="Up to 20 ids starting with G, M or C, one per line."
                className="font-mono text-small"
                autoCapitalize="characters"
                spellCheck={false}
                value={values.stellarIds}
                onChange={set("stellarIds")}
                error={errors.stellarIds}
              />
              <TextField
                label="Stellar Community Fund round"
                name="scf_round_url"
                optional
                type="url"
                inputMode="url"
                placeholder="https://"
                hint="A link to the project's SCF submission, if it has one."
                autoCapitalize="none"
                spellCheck={false}
                value={values.scfRoundUrl}
                onChange={set("scfRoundUrl")}
                error={errors.scfRoundUrl}
              />
            </fieldset>

            <fieldset className={fieldset}>
              <legend className={legend}>SCF cohort (optional)</legend>
              <p className="text-small text-fg-2">
                If the Stellar Community Fund awarded your project, propose the program, round,
                category and award date. An operator confirms them; until then your project is
                compared with every enrolled project.
              </p>
              <CohortFields
                values={cohort}
                onChange={setCohort}
                errors={cohortErrors}
                idPrefix="enroll-cohort"
              />
            </fieldset>

            <div className="flex flex-col gap-2">
              <div className="flex items-start gap-3">
                <Checkbox
                  id={termsId}
                  checked={values.termsAccepted}
                  onCheckedChange={(c) => setValues((s) => ({ ...s, termsAccepted: c === true }))}
                  aria-invalid={errors.termsAccepted ? true : undefined}
                  aria-describedby={errors.termsAccepted ? `${termsId}-e` : undefined}
                  className="mt-0.5"
                />
                <label htmlFor={termsId} className="text-body text-fg">
                  I accept the{" "}
                  <a
                    href="/terms"
                    target="_blank"
                    rel="noopener"
                    className="rounded-sm underline decoration-line-strong underline-offset-2 transition-colors duration-(--vs-duration-press) hover:decoration-fg focus-ring"
                  >
                    terms of the service
                  </a>{" "}
                  (version <span className="font-mono text-small">{termsVersion}</span>) for this
                  project.
                </label>
              </div>
              {errors.termsAccepted ? (
                <p
                  id={`${termsId}-e`}
                  role="alert"
                  className="animate-fade-in pl-7 text-caption font-normal text-critical"
                >
                  {errors.termsAccepted}
                </p>
              ) : null}
            </div>

            <div className="flex flex-wrap items-center gap-x-3 gap-y-3">
              <Button
                type="submit"
                pending={enroll.isPending}
                disabled={enroll.isPending}
                className="max-sm:h-(--vs-control-touch) max-sm:flex-1"
              >
                {enroll.isPending ? "Enrolling" : "Enroll project"}
              </Button>
              <Button asChild variant="ghost" className="max-sm:h-(--vs-control-touch)">
                <Link to="/">Cancel</Link>
              </Button>
              {errorCount > 0 ? (
                <span className="text-caption font-normal text-critical sm:ml-auto">
                  {errorCount === 1
                    ? "1 field needs attention"
                    : `${errorCount} fields need attention`}
                </span>
              ) : null}
              {otherError ? <FormMessage error={otherError} className="w-full" /> : null}
            </div>
          </form>
        </div>

        <aside aria-labelledby={`${termsId}-after`} className="flex flex-col xl:pt-24">
          <h2
            id={`${termsId}-after`}
            className="pb-2 text-caption tracking-wide text-fg-3 uppercase"
          >
            After you enroll
          </h2>
          <ol className="flex flex-col">
            {AFTER.map((step, i) => (
              <li
                key={step.title}
                className="grid grid-cols-[1.5rem_minmax(0,1fr)] gap-2.5 border-t border-line py-3"
              >
                <span className="pt-0.5 font-mono text-caption font-normal text-fg-3">{i + 1}</span>
                <span className="flex flex-col gap-0.5">
                  <span className="text-body font-medium text-fg">{step.title}</span>
                  <span className="text-small text-fg-2">{step.text}</span>
                </span>
              </li>
            ))}
          </ol>
        </aside>
      </div>
    </section>
  );
}

// what happens next, as the console does it: a pending project waits for
// review (when the service asks for approval), sites come next, the api is
// optional
const AFTER = [
  {
    title: "Operator review",
    text: "When the service asks for approval, operators read the answers. You can add sites and members meanwhile.",
  },
  { title: "Add your website", text: "Each site gets a tracking snippet to install." },
  {
    title: "Connect your API",
    text: "Optional: send API metrics with an OpenTelemetry exporter and a token.",
  },
];
