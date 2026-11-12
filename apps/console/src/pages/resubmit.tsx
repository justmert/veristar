import { useMutation } from "@tanstack/react-query";
import { type FormEvent, useState } from "react";
import { ApiError } from "@/api/client";
import { api, type ResubmitInput } from "@/api/endpoints";
import { keys } from "@/api/queries";
import type { Tenant } from "@/api/schemas";
import { FormMessage } from "@/components/error-panel";
import { TextAreaField, TextField } from "@/components/form-field";
import { Button } from "@/components/ui/button";
import * as v from "@/lib/validate";
import { queryClient } from "@/query-client";
import { splitIds } from "./enroll";

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
  if (ids.length > 20) errors.stellarIds = "At most 20 ids.";
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

// the resubmit form inside the rejected banner (contract 5.10)
export function ResubmitForm({ tenant }: { tenant: Tenant }) {
  const [open, setOpen] = useState(false);
  const [values, setValues] = useState<ResubmitValues>(() => resubmitValues(tenant));
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
      <Button size="sm" className="mt-3" onClick={() => setOpen(true)}>
        Edit and resubmit
      </Button>
    );

  const set = (k: Key) => (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) =>
    setValues((s) => ({ ...s, [k]: e.target.value }));

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
      className="mt-4 space-y-4 rounded-lg border bg-card p-4"
    >
      <TextField
        label="Project name"
        name="name"
        maxLength={100}
        value={values.name}
        onChange={set("name")}
        error={errors.name}
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
        label="Website"
        name="website"
        type="url"
        inputMode="url"
        autoCapitalize="none"
        spellCheck={false}
        value={values.website}
        onChange={set("website")}
        error={errors.website}
      />
      <TextAreaField
        label="How does the project use Stellar?"
        name="stellar_usage"
        rows={3}
        maxLength={2000}
        value={values.stellarUsage}
        onChange={set("stellarUsage")}
        error={errors.stellarUsage}
      />
      <TextAreaField
        label="Account or contract ids"
        name="stellar_ids"
        optional
        rows={3}
        className="font-mono text-xs"
        spellCheck={false}
        hint="Up to 20 ids starting with G, M or C, one per line."
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
        autoCapitalize="none"
        spellCheck={false}
        value={values.scfRoundUrl}
        onChange={set("scfRoundUrl")}
        error={errors.scfRoundUrl}
      />
      <div className="flex flex-wrap items-center gap-3">
        <Button type="submit" size="sm" disabled={send.isPending}>
          {send.isPending ? "Submitting" : "Submit for review"}
        </Button>
        <Button type="button" variant="ghost" size="sm" onClick={() => setOpen(false)}>
          Cancel
        </Button>
        <FormMessage
          error={
            send.error && Object.keys(serverErrors(send.error)).length === 0 ? send.error : null
          }
        />
      </div>
    </form>
  );
}
