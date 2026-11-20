import { useMutation } from "@tanstack/react-query";
import { PencilLine } from "lucide-react";
import { type FormEvent, useState } from "react";
import { ApiError } from "@/api/client";
import { api, type ResubmitInput } from "@/api/endpoints";
import { keys } from "@/api/queries";
import type { Tenant } from "@/api/schemas";
import { FormMessage } from "@/components/error-panel";
import { TextAreaField, TextField } from "@/components/form-field";
import { Notice } from "@/components/notice";
import { Button } from "@/components/ui/button";
import { formatDate } from "@/lib/format";
import * as v from "@/lib/validate";
import { NumberedSection, StellarFields, splitIds } from "@/pages/home/enroll";
import { queryClient } from "@/query-client";

export type ResubmitValues = {
  name: string;
  description: string;
  website: string;
  stellarUsage: string;
  stellarIds: string;
  scfRoundUrl: string;
};
type Key = keyof ResubmitValues;
export type ResubmitErrors = Partial<Record<Key, string>>;

export function resubmitValues(t: Tenant): ResubmitValues {
  const a = t.enrollment_answers;
  return {
    name: t.name,
    description: t.description,
    website: a.website,
    stellarUsage: a.stellar_usage,
    stellarIds: a.stellar_ids.join("\n"),
    scfRoundUrl: a.scf_round_url ?? "",
  };
}

export function validateResubmit(values: ResubmitValues): ResubmitErrors {
  const errors: ResubmitErrors = {};
  const put = (k: Key, code: string | null) => {
    if (code && !errors[k]) errors[k] = v.fieldMessage(code);
  };
  put("name", v.tenantName(values.name));
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
  return errors;
}

// contract 5.7: enrollment is required, name and description are sent only
// when they changed
export function resubmitBody(t: Tenant, values: ResubmitValues): ResubmitInput {
  const ids = splitIds(values.stellarIds);
  const scf = values.scfRoundUrl.trim();
  const name = values.name.trim();
  const description = values.description.trim();
  return {
    ...(name !== t.name ? { name } : {}),
    ...(description !== t.description ? { description } : {}),
    enrollment: {
      website: values.website.trim(),
      stellar_usage: values.stellarUsage.trim(),
      ...(ids.length ? { stellar_ids: ids } : {}),
      ...(scf ? { scf_round_url: scf } : {}),
    },
  };
}

const PATHS: Record<string, Key> = {
  name: "name",
  description: "description",
  "enrollment.website": "website",
  "enrollment.stellar_usage": "stellarUsage",
  "enrollment.stellar_ids": "stellarIds",
  "enrollment.scf_round_url": "scfRoundUrl",
};

function serverErrors(err: unknown): ResubmitErrors {
  if (!(err instanceof ApiError) || err.code !== "validation_failed") return {};
  const out: ResubmitErrors = {};
  for (const [path, code] of Object.entries(err.fields)) {
    const key = PATHS[path] ?? (path.startsWith("enrollment.stellar_ids[") ? "stellarIds" : null);
    if (key && !out[key]) out[key] = v.fieldMessage(code);
  }
  return out;
}

// the rejected notice of an owner, which opens into the resubmit form
// (contract 5.10): the reviewer's reason on top, the answers below
export function ResubmitForm({ tenant }: { tenant: Tenant }) {
  const [open, setOpen] = useState(false);
  const [initial] = useState<ResubmitValues>(() => resubmitValues(tenant));
  const [values, setValues] = useState<ResubmitValues>(initial);
  const [errors, setErrors] = useState<ResubmitErrors>({});
  const send = useMutation({
    mutationFn: (body: ResubmitInput) => api.resubmit(tenant.id, body),
    onSuccess: async (t) => {
      queryClient.setQueryData(keys.tenant(t.id), t);
      await queryClient.invalidateQueries({ queryKey: keys.me });
      setOpen(false);
    },
    onError: (err) => setErrors(serverErrors(err)),
  });

  if (!open)
    return (
      <Notice
        tone="critical"
        title="Enrollment was not approved"
        actions={
          <Button size="sm" className="max-sm:h-(--vs-control-touch)" onClick={() => setOpen(true)}>
            Edit and resubmit
          </Button>
        }
      >
        {tenant.status_reason ? `Reason given: “${tenant.status_reason}”` : "No reason was given."}
      </Notice>
    );

  const set = (k: Key) => (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) =>
    setValues((s) => ({ ...s, [k]: e.target.value }));

  // a quiet marker on each answer that differs from the one on file, so the
  // owner sees what the reviewers will read anew
  const changed = (k: Key) =>
    values[k].trim() !== initial[k].trim() ? (
      <span className="flex animate-fade-in items-center gap-1 text-warning">
        <PencilLine aria-hidden className="size-3" />
        Changed from the answers on file
      </span>
    ) : undefined;

  function submit(e: FormEvent) {
    e.preventDefault();
    const found = validateResubmit(values);
    setErrors(found);
    if (Object.keys(found).length > 0) return;
    send.mutate(resubmitBody(tenant, values));
  }

  return (
    <form
      noValidate
      onSubmit={submit}
      aria-label="Resubmit the enrollment"
      className="flex max-w-180 animate-rise-in flex-col gap-5"
    >
      <Notice tone="critical" title="Enrollment was not approved">
        {tenant.status_reason ? (
          <span className="flex flex-col gap-1">
            <span>Reason given on {formatDate(tenant.status_changed_at)}</span>
            <q className="text-fg">{tenant.status_reason}</q>
          </span>
        ) : (
          "No reason was given."
        )}
      </Notice>
      <NumberedSection n={1} title="Project">
        <TextField
          label="Name"
          name="name"
          maxLength={100}
          value={values.name}
          onChange={set("name")}
          error={errors.name}
          hint={changed("name")}
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
          hint={changed("website")}
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
          hint={changed("description")}
        />
      </NumberedSection>
      <NumberedSection n={2} title="Stellar">
        <StellarFields
          usage={values.stellarUsage}
          ids={values.stellarIds}
          onUsage={set("stellarUsage")}
          onIds={set("stellarIds")}
          usageError={errors.stellarUsage}
          idsError={errors.stellarIds}
        />
        {changed("stellarUsage") || changed("stellarIds") ? (
          <span className="text-caption">{changed("stellarUsage") ?? changed("stellarIds")}</span>
        ) : null}
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
          hint={changed("scfRoundUrl")}
        />
      </NumberedSection>
      <div className="flex flex-col gap-3 sm:pl-10">
        <FormMessage
          error={
            send.error && Object.keys(serverErrors(send.error)).length === 0 ? send.error : null
          }
        />
        <div className="flex flex-wrap items-center gap-3">
          <Button
            type="submit"
            pending={send.isPending}
            disabled={send.isPending}
            className="h-10 px-5 max-sm:h-(--vs-control-touch) max-sm:flex-1"
          >
            {send.isPending ? "Submitting" : "Submit for review"}
          </Button>
          <Button
            type="button"
            variant="ghost"
            className="max-sm:h-(--vs-control-touch)"
            onClick={() => setOpen(false)}
          >
            Cancel
          </Button>
        </div>
      </div>
    </form>
  );
}
