import { useMutation, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Link, useParams } from "react-router";
import { api } from "@/api/endpoints";
import { keys, verificationQuery } from "@/api/queries";
import type { VerifyMethod } from "@/api/schemas";
import { CopyButton } from "@/components/copy-button";
import { ErrorPanel, FormMessage, Loading } from "@/components/error-panel";
import { VerificationDetail } from "@/components/evidence";
import { SelectField } from "@/components/form-field";
import { Button } from "@/components/ui/button";
import { isNotFound } from "@/lib/errors";
import { queryClient } from "@/query-client";
import { NotFoundPage } from "./not-found";
import { useProject } from "./project-layout";

type Choice = "both" | VerifyMethod;

// contract 5.8: proves control of the site's domain with a dns txt record or a
// file under /.well-known. verification never gates traffic; operators see it
// as evidence.
export function VerifyPage() {
  const { tenant } = useProject();
  const { siteId = "" } = useParams();
  const info = useQuery(verificationQuery(siteId));
  const [choice, setChoice] = useState<Choice>("both");
  const verify = useMutation({
    mutationFn: () => api.verifySite(siteId, choice === "both" ? null : choice),
    onSuccess: async (v) => {
      queryClient.setQueryData(keys.verification(siteId), v);
      await queryClient.invalidateQueries({ queryKey: keys.sites(tenant.id) });
    },
  });

  if (info.isPending) return <Loading label="Loading the verification instructions" />;
  if (isNotFound(info.error)) return <NotFoundPage />;
  if (info.error) return <ErrorPanel error={info.error} onRetry={() => info.refetch()} />;
  const v = info.data;

  return (
    <section className="max-w-3xl space-y-6">
      <div>
        <Link
          to={`/projects/${tenant.id}/sites`}
          className="text-sm text-muted-foreground hover:text-foreground"
        >
          Sites
        </Link>
        <h2 className="mt-1 text-xl font-semibold tracking-tight">Verify {v.domain}</h2>
        <p className="mt-1 max-w-prose text-sm text-muted-foreground">
          Show that this project controls the domain by adding one of the two records below. Either
          one is enough. Verification is shown to operators of the service and does not change how
          events are collected.
        </p>
      </div>

      <div className="rounded-xl border bg-card p-4 sm:p-5">
        <h3 className="font-semibold">Current state</h3>
        <div className="mt-2">
          <VerificationDetail
            verification={v.verification}
            verifyMethod={v.verify_method}
            verifiedAt={v.verified_at}
            lastVerifyAt={v.last_verify_at}
            lastVerifyError={v.last_verify_error}
          />
        </div>
      </div>

      <article aria-labelledby="verify-dns" className="rounded-xl border bg-card p-4 sm:p-5">
        <h3 id="verify-dns" className="font-semibold">
          Option 1: DNS TXT record
        </h3>
        <p className="mt-1 text-sm text-muted-foreground">
          Add this record at your DNS provider. Changes can take a while to propagate.
        </p>
        <dl className="mt-3 space-y-3 text-sm">
          <Row label="Name" value={v.dns.name} />
          <Row label="Type" value={v.dns.type} copy={false} />
          <Row label="Value" value={v.dns.value} />
        </dl>
      </article>

      <article aria-labelledby="verify-file" className="rounded-xl border bg-card p-4 sm:p-5">
        <h3 id="verify-file" className="font-semibold">
          Option 2: file on the website
        </h3>
        <p className="mt-1 text-sm text-muted-foreground">
          Serve this text over HTTPS at the address below, with status 200 and nothing else in the
          file. Redirects to another host are not followed.
        </p>
        <dl className="mt-3 space-y-3 text-sm">
          <Row label="Address" value={v.file.url} />
          <Row label="Content" value={v.file.body} />
        </dl>
      </article>

      <div className="flex flex-wrap items-end gap-3">
        <SelectField
          label="Check"
          value={choice}
          onChange={(e) => setChoice(e.target.value as Choice)}
        >
          <option value="both">DNS first, then the file</option>
          <option value="dns">Only the DNS record</option>
          <option value="file">Only the file</option>
        </SelectField>
        <Button onClick={() => verify.mutate()} disabled={verify.isPending}>
          {verify.isPending ? "Checking" : "Verify now"}
        </Button>
        <span role="status" className="text-sm">
          {verify.isSuccess
            ? verify.data.verification === "verified" && verify.data.last_verify_error === null
              ? "Verified."
              : "Not verified yet. See the current state above."
            : ""}
        </span>
      </div>
      <FormMessage error={verify.error} />
      <p className="text-xs text-muted-foreground">
        A check takes up to 15 seconds. At most 6 checks per site and hour. The domain is also
        checked again once a day.
      </p>
    </section>
  );
}

function Row({ label, value, copy = true }: { label: string; value: string; copy?: boolean }) {
  return (
    <div className="grid gap-1 sm:grid-cols-[6rem_1fr] sm:items-start">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="flex min-w-0 flex-wrap items-center gap-2">
        <code className="min-w-0 break-all rounded-md bg-muted px-2 py-1 font-mono text-xs">
          {value}
        </code>
        {copy ? <CopyButton text={value} label={`Copy ${label.toLowerCase()}`} /> : null}
      </dd>
    </div>
  );
}
