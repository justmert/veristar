import { useMutation, useQuery } from "@tanstack/react-query";
import { type FormEvent, useEffect, useId, useRef, useState } from "react";
import { Link, useNavigate } from "react-router";
import { ApiError } from "@/api/client";
import { api, type EnrollmentInput } from "@/api/endpoints";
import { keys, meQuery } from "@/api/queries";
import { ErrorPanel, FormMessage, Loading } from "@/components/error-panel";
import { TextAreaField, TextField } from "@/components/form-field";
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
    setErrors(found);
    if (Object.keys(found).length > 0) {
      setFocusTick((n) => n + 1);
      return;
    }
    enroll.mutate(enrollmentBody(values, termsVersion));
  }

  const otherError =
    enroll.error && Object.keys(serverEnrollErrors(enroll.error, values)).length === 0
      ? enroll.error
      : null;

  return (
    <section className="max-w-2xl">
      <Link to="/" className="text-sm text-muted-foreground hover:text-foreground">
        Your projects
      </Link>
      <h1 className="mt-2 text-3xl font-semibold tracking-tight">Enroll a project</h1>
      <p className="mt-2 text-muted-foreground">
        Tell us about the project. Operators of this service read these answers to keep it for the
        Stellar ecosystem.
      </p>

      <form
        ref={formRef}
        noValidate
        onSubmit={submit}
        className="mt-8 space-y-8"
        aria-label="Enroll a project"
      >
        <fieldset className="space-y-5 rounded-xl border bg-card p-5 sm:p-6 [&>legend+*]:clear-both">
          <legend className="float-left mb-4 w-full text-base font-semibold tracking-tight">
            The project
          </legend>
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
            maxLength={63}
            value={values.slug}
            onChange={set("slug")}
            error={errors.slug}
          />
          <TextAreaField
            label="Description"
            name="description"
            optional
            rows={3}
            maxLength={1000}
            value={values.description}
            onChange={set("description")}
            error={errors.description}
          />
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
        </fieldset>

        <fieldset className="space-y-5 rounded-xl border bg-card p-5 sm:p-6 [&>legend+*]:clear-both">
          <legend className="float-left mb-4 w-full text-base font-semibold tracking-tight">
            Its place in the Stellar ecosystem
          </legend>
          <TextAreaField
            label="How does the project use Stellar?"
            name="stellar_usage"
            rows={4}
            maxLength={2000}
            hint="For example: a Soroban lending protocol on mainnet, or a wallet for Stellar accounts."
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
            className="font-mono text-xs"
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

        <div className="space-y-2">
          <div className="flex items-start gap-3">
            <Checkbox
              id={termsId}
              checked={values.termsAccepted}
              onCheckedChange={(c) => setValues((s) => ({ ...s, termsAccepted: c === true }))}
              aria-invalid={errors.termsAccepted ? true : undefined}
              aria-describedby={errors.termsAccepted ? `${termsId}-e` : undefined}
              className="mt-0.5"
            />
            <label htmlFor={termsId} className="text-sm leading-snug">
              I accept the{" "}
              <a
                href="/terms"
                target="_blank"
                rel="noopener"
                className="text-primary underline underline-offset-2"
              >
                terms of the service
              </a>{" "}
              (version {termsVersion}) for this project.
            </label>
          </div>
          {errors.termsAccepted ? (
            <p id={`${termsId}-e`} role="alert" className="pl-7 text-sm text-destructive">
              {errors.termsAccepted}
            </p>
          ) : null}
        </div>

        <div className="flex flex-wrap items-center gap-3">
          <Button type="submit" size="lg" className="px-4" disabled={enroll.isPending}>
            {enroll.isPending ? "Enrolling" : "Enroll project"}
          </Button>
          <FormMessage error={otherError} />
        </div>
      </form>
    </section>
  );
}
