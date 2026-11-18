import { useMutation, useQuery } from "@tanstack/react-query";
import { fade } from "@veristar/brand/motion";
import { Check, CircleAlert, LoaderCircle } from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
import { useId, useState } from "react";
import { api } from "@/api/endpoints";
import { keys, verificationQuery } from "@/api/queries";
import type { Site, SiteVerification, VerifyMethod } from "@/api/schemas";
import { CopyButton } from "@/components/copy-button";
import { FormMessage } from "@/components/error-panel";
import { verifyAdvice, verifyErrorText } from "@/components/evidence";
import { NativeSelect } from "@/components/form-field";
import { ErrorState } from "@/components/states";
import { toast } from "@/components/toast";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { formatDateTimeUtc } from "@/lib/format";
import { queryClient } from "@/query-client";

type Choice = "both" | VerifyMethod;

const METHOD_TEXT: Record<VerifyMethod, string> = { dns: "DNS record", file: "file" };

export type VerifyLine = {
  tone: "checking" | "failed" | "verified" | "idle";
  title: string;
  body: string | null;
};

// the line under the check button: what is happening now, or the last
// result with what to do about it
export function verifyLine(v: SiteVerification, checking: Choice | null): VerifyLine {
  if (checking)
    return {
      tone: "checking",
      title:
        checking === "file"
          ? `Fetching ${v.file.url}`
          : checking === "dns"
            ? `Looking up TXT records for ${v.dns.name}`
            : `Looking up TXT records for ${v.dns.name}, then fetching the file`,
      body: "This takes up to 15 seconds.",
    };
  if (v.last_verify_error)
    return {
      tone: "failed",
      title: verifyErrorText(v.last_verify_error),
      body: verifyAdvice(v.last_verify_error),
    };
  if (v.verification === "verified")
    return {
      tone: "verified",
      title: `Verified by ${v.verify_method ? METHOD_TEXT[v.verify_method] : "the check"}`,
      body: "Keep the record or file in place. The daily check reads it again.",
    };
  return { tone: "idle", title: "Add the record or the file, then check.", body: null };
}

// contract 5.8: proves control of the site's domain with a dns txt record or
// a file under /.well-known. verification never gates traffic; operators see
// it as evidence. one step of the site's setup; `current` gives its check
// button the accent while it is the step to do now.
export function VerifyDomain({ site, current = false }: { site: Site; current?: boolean }) {
  const info = useQuery(verificationQuery(site.id));
  if (info.isPending)
    return (
      <div role="status" aria-busy="true" className="flex flex-col gap-3">
        <span className="sr-only">Loading the verification steps</span>
        <Skeleton className="h-9 w-1/2" />
        <Skeleton className="h-24 w-full" />
      </div>
    );
  if (info.error)
    return (
      <ErrorState
        title="The domain check did not load"
        error={info.error}
        onRetry={() => info.refetch()}
        compact
      />
    );
  return <Verify v={info.data} tenantId={site.tenant_id} current={current} />;
}

function Verify({
  v,
  tenantId,
  current,
}: {
  v: SiteVerification;
  tenantId: string;
  current: boolean;
}) {
  const siteId = v.site_id;
  const choiceId = useId();
  const [method, setMethod] = useState<VerifyMethod>(v.verify_method ?? "dns");
  const [choice, setChoice] = useState<Choice>("both");
  const verify = useMutation({
    mutationFn: () => api.verifySite(siteId, choice === "both" ? null : choice),
    onSuccess: async (next) => {
      queryClient.setQueryData(keys.verification(siteId), next);
      if (next.verification === "verified" && next.last_verify_error === null)
        toast.success(`${v.domain} is verified.`);
      await queryClient.invalidateQueries({ queryKey: keys.sites(tenantId) });
    },
  });

  const verified = v.verification === "verified" && v.last_verify_error === null;
  const line = verifyLine(v, verify.isPending ? choice : null);

  return (
    <div className="flex min-w-0 flex-col gap-4">
      <Tabs
        value={method}
        onValueChange={(m) => setMethod(m as VerifyMethod)}
        className="min-w-0 gap-3"
      >
        <TabsList aria-label="Method" className="max-w-full">
          <TabsTrigger value="dns" className="max-lg:h-(--vs-control-touch)">
            DNS record
          </TabsTrigger>
          <TabsTrigger value="file" className="max-lg:h-(--vs-control-touch)">
            File on the site
          </TabsTrigger>
        </TabsList>
        <TabsContent
          forceMount
          value="dns"
          className="flex flex-col gap-3 data-[state=inactive]:hidden"
        >
          <p className="max-w-prose text-small text-fg-2">
            Add this record where you manage the DNS of {v.domain}. A change can take some time to
            show.
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
          className="flex flex-col gap-3 data-[state=inactive]:hidden"
        >
          <p className="max-w-prose text-small text-fg-2">
            Serve this text over HTTPS at the address below, with status 200. Put nothing else in
            the file. The check does not follow a redirect to another host.
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

      <div className="flex flex-wrap items-end gap-3">
        <div className="flex flex-col gap-1">
          <label htmlFor={choiceId} className="text-small font-medium text-fg">
            Check
          </label>
          <NativeSelect
            id={choiceId}
            value={choice}
            onChange={(e) => setChoice(e.target.value as Choice)}
            className="max-lg:h-(--vs-control-touch)"
          >
            <option value="both">DNS record, then the file</option>
            <option value="dns">Only the DNS record</option>
            <option value="file">Only the file</option>
          </NativeSelect>
        </div>
        <Button
          variant={current && !verified ? "default" : "outline"}
          onClick={() => verify.mutate()}
          pending={verify.isPending}
          className="max-lg:h-(--vs-control-touch)"
        >
          {verify.isPending ? "Checking" : verified ? "Check again" : "Check now"}
        </Button>
      </div>

      <div aria-live="polite" className="min-h-12">
        <AnimatePresence mode="wait" initial={false}>
          <motion.div key={`${line.tone}-${line.title}`} {...fade} className="flex gap-2">
            <span className="mt-1 flex">
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
            <span className="flex min-w-0 flex-col gap-1 text-small">
              <span className="break-words text-fg">{line.title}</span>
              {line.body ? <span className="text-fg-2">{line.body}</span> : null}
              {!verify.isPending && v.last_verify_at ? (
                <span className="text-fg-2">
                  Last checked {formatDateTimeUtc(v.last_verify_at)}.
                  {verified && v.verified_at
                    ? ` Verified ${formatDateTimeUtc(v.verified_at)}.`
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
      <p className="text-caption text-fg-2">
        You can check 6 times per hour. The service also checks the domain once a day.
      </p>
    </div>
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
          className="grid grid-cols-[4rem_minmax(0,1fr)_auto] items-center gap-x-3 px-3 py-2"
        >
          <dt className="text-caption text-fg-2">{r.k}</dt>
          <dd className="min-w-0 break-all font-mono text-small text-fg">{r.value}</dd>
          <span className="flex">
            {r.copy === false ? null : (
              <CopyButton
                text={r.value}
                label={`Copy ${r.k.toLowerCase()}`}
                size="xs"
                variant="ghost"
                iconOnly
              />
            )}
          </span>
        </div>
      ))}
    </dl>
  );
}
