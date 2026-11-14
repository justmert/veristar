import { useMutation, useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { fade } from "@veristar/brand/motion";
import { Check, CircleAlert, LoaderCircle } from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
import { useState } from "react";
import { useParams } from "react-router";
import { api } from "@/api/endpoints";
import { keys, sitesQuery, verificationQuery } from "@/api/queries";
import type { SiteVerification, VerifyMethod } from "@/api/schemas";
import { CopyButton } from "@/components/copy-button";
import { ErrorPanel, FormMessage, Loading } from "@/components/error-panel";
import {
  StellarTomlSummary,
  VerificationPill,
  verifyAdvice,
  verifyErrorText,
} from "@/components/evidence";
import { NativeSelect } from "@/components/form-field";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { isNotFound } from "@/lib/errors";
import { formatDateTimeUtc } from "@/lib/format";
import { queryClient } from "@/query-client";
import { NotFoundPage } from "./not-found";
import { useProject } from "./project-layout";
import { SetupFrame, useFirstEvent } from "./snippet";

type Choice = "both" | VerifyMethod;

const METHOD_TEXT: Record<VerifyMethod, string> = { dns: "DNS record", file: "file" };

// contract 5.8: proves control of the site's domain with a dns txt record or a
// file under /.well-known. verification never gates traffic; operators see it
// as evidence.
export function VerifyPage() {
  const { tenant } = useProject();
  const { siteId = "" } = useParams();
  const sites = useQuery(sitesQuery(tenant.id));
  const info = useQuery(verificationQuery(siteId));
  const first = useFirstEvent(siteId);
  const site = sites.data?.find((s) => s.id === siteId);

  if (isNotFound(info.error)) return <NotFoundPage />;

  return (
    <SetupFrame site={site} step="verify" event={first.event}>
      {info.isPending ? <Loading label="Loading the verification instructions" /> : null}
      {info.error ? <ErrorPanel error={info.error} onRetry={() => info.refetch()} /> : null}
      {info.data ? <Verify v={info.data} tenantId={tenant.id} /> : null}
      {site ? (
        <section
          aria-label="stellar.toml"
          className="rounded-lg border border-line bg-raised px-4 py-3.5 sm:px-5"
        >
          <StellarTomlSummary
            toml={site.stellar_toml}
            checkedAt={site.stellar_toml_checked_at}
            domain={site.domain}
          />
        </section>
      ) : null}
    </SetupFrame>
  );
}

function Verify({ v, tenantId }: { v: SiteVerification; tenantId: string }) {
  const siteId = v.site_id;
  const [method, setMethod] = useState<VerifyMethod>(v.verify_method ?? "dns");
  const [choice, setChoice] = useState<Choice>("both");
  const verify = useMutation({
    mutationFn: () => api.verifySite(siteId, choice === "both" ? null : choice),
    onSuccess: async (next) => {
      queryClient.setQueryData(keys.verification(siteId), next);
      await queryClient.invalidateQueries({ queryKey: keys.sites(tenantId) });
    },
  });

  const verified = v.verification === "verified" && v.last_verify_error === null;
  // the line under the button: what is happening now, or the last result
  const line = verify.isPending
    ? {
        tone: "checking" as const,
        title:
          choice === "file"
            ? `Fetching ${v.file.url}`
            : choice === "dns"
              ? `Looking up TXT records for ${v.dns.name}`
              : `Looking up TXT records for ${v.dns.name}, then fetching the file`,
        body: "This takes up to 15 seconds.",
      }
    : v.last_verify_error
      ? {
          tone: "failed" as const,
          title: verifyErrorText(v.last_verify_error),
          body: verifyAdvice(v.last_verify_error),
        }
      : verified
        ? {
            tone: "verified" as const,
            title: `Verified by ${v.verify_method ? METHOD_TEXT[v.verify_method] : "the check"}`,
            body: "Keep the record or file in place so the daily check keeps passing.",
          }
        : {
            tone: "idle" as const,
            title: "Add the record, then check.",
            body: null,
          };

  return (
    <section
      aria-labelledby="verify-title"
      className="flex flex-col rounded-lg border border-line bg-raised"
    >
      <header className="flex flex-wrap items-start justify-between gap-3 px-4 pt-4 sm:px-5">
        <div className="flex min-w-0 flex-col gap-0.5">
          <h2 id="verify-title" className="text-title text-fg">
            Prove you control <span className="break-all">{v.domain}</span>
          </h2>
          <p className="text-small text-fg-2">
            Either method is enough. Verification is shown to operators of the service and does not
            change how events are collected.
          </p>
        </div>
        <VerificationPill verification={v.verification} live={verify.isPending} />
      </header>

      <Tabs
        value={method}
        onValueChange={(m) => setMethod(m as VerifyMethod)}
        className="mt-3 gap-0"
      >
        <TabsList variant="line" aria-label="Method" className="px-2 sm:px-3">
          <TabsTrigger value="dns">DNS record</TabsTrigger>
          <TabsTrigger value="file">File on the site</TabsTrigger>
        </TabsList>
        <TabsContent
          forceMount
          value="dns"
          className="data-[state=inactive]:hidden flex flex-col gap-3 px-4 pt-4 sm:px-5"
        >
          <p className="text-small text-fg-2">
            Add this record where you manage DNS for {v.domain}, at your registrar or DNS host.
            Changes can take a while to propagate.
          </p>
          <RecordTable
            label="DNS TXT record"
            rows={[
              { k: "Type", value: v.dns.type, copy: false },
              { k: "Name", value: v.dns.name },
              { k: "Value", value: v.dns.value },
            ]}
          />
        </TabsContent>
        <TabsContent
          forceMount
          value="file"
          className="data-[state=inactive]:hidden flex flex-col gap-3 px-4 pt-4 sm:px-5"
        >
          <p className="text-small text-fg-2">
            Serve this text over HTTPS at the address below, with status 200 and nothing else in the
            file. Redirects to another host are not followed.
          </p>
          <RecordTable
            label="Verification file"
            rows={[
              { k: "Address", value: v.file.url },
              { k: "Content", value: v.file.body },
            ]}
          />
        </TabsContent>
      </Tabs>

      <div className="mt-4 flex flex-col gap-3 border-t border-line px-4 py-4 sm:px-5">
        <div className="flex flex-wrap items-end gap-3">
          <div className="flex flex-col gap-1.5">
            <label htmlFor="verify-choice" className="text-small font-medium text-fg">
              Check
            </label>
            <NativeSelect
              id="verify-choice"
              value={choice}
              onChange={(e) => setChoice(e.target.value as Choice)}
            >
              <option value="both">DNS first, then the file</option>
              <option value="dns">Only the DNS record</option>
              <option value="file">Only the file</option>
            </NativeSelect>
          </div>
          <Button
            onClick={() => verify.mutate()}
            pending={verify.isPending}
            className="max-lg:h-(--vs-control-touch)"
          >
            {verify.isPending ? "Checking" : verified ? "Check again" : "Check now"}
          </Button>
        </div>
        <div aria-live="polite" className="min-h-12">
          <AnimatePresence mode="wait" initial={false}>
            <motion.div key={`${line.tone}-${line.title}`} {...fade} className="flex gap-2.5">
              <span className="mt-0.5 flex">
                {line.tone === "checking" ? (
                  <LoaderCircle aria-hidden className="size-4 animate-spin text-fg-2" />
                ) : line.tone === "failed" ? (
                  <CircleAlert aria-hidden className="size-4 text-warning" />
                ) : line.tone === "verified" ? (
                  <Check aria-hidden className="size-4 text-success" strokeWidth={2.6} />
                ) : (
                  <span aria-hidden className="m-1 size-2 rounded-full bg-fg-3" />
                )}
              </span>
              <span className="flex min-w-0 flex-col gap-0.5 text-small">
                <span
                  className={cn(
                    "break-words",
                    line.tone === "failed"
                      ? "text-warning"
                      : line.tone === "verified"
                        ? "text-success"
                        : "text-fg",
                  )}
                >
                  {line.title}
                </span>
                {line.body ? <span className="text-fg-2">{line.body}</span> : null}
                {!verify.isPending && v.last_verify_at ? (
                  <span className="text-fg-3">
                    Last checked {formatDateTimeUtc(v.last_verify_at)}
                    {verified && v.verified_at
                      ? `, verified ${formatDateTimeUtc(v.verified_at)}`
                      : ""}
                  </span>
                ) : null}
              </span>
            </motion.div>
          </AnimatePresence>
        </div>
        <span role="status" className="sr-only">
          {verify.isSuccess
            ? verify.data.verification === "verified" && verify.data.last_verify_error === null
              ? "Verified."
              : "Not verified yet."
            : ""}
        </span>
        <FormMessage error={verify.error} />
        <p className="text-caption font-normal text-fg-3">
          A check takes up to 15 seconds. At most 6 checks per site and hour. The domain is also
          checked again once a day.
        </p>
      </div>
    </section>
  );
}

// records as a table with copy per field, on the sunken surface that reads
// "value to copy"
function RecordTable({
  label,
  rows,
}: {
  label: string;
  rows: { k: string; value: string; copy?: boolean }[];
}) {
  return (
    <dl
      aria-label={label}
      className="flex flex-col divide-y divide-line overflow-hidden rounded-md border border-line bg-sunken"
    >
      {rows.map((r) => (
        <div
          key={r.k}
          className="grid grid-cols-[5rem_minmax(0,1fr)] items-center gap-x-3 gap-y-1 px-3 py-2 sm:grid-cols-[5rem_minmax(0,1fr)_auto]"
        >
          <dt className="text-caption text-fg-3">{r.k}</dt>
          <dd className="min-w-0 break-all font-mono text-small text-fg">{r.value}</dd>
          {r.copy === false ? (
            <span className="max-sm:hidden" />
          ) : (
            <span className="col-start-2 sm:col-start-auto">
              <CopyButton
                text={r.value}
                label={`Copy ${r.k.toLowerCase()}`}
                size="xs"
                variant="ghost"
              />
            </span>
          )}
        </div>
      ))}
    </dl>
  );
}
