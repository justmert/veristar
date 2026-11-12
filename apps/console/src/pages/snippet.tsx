import { useQuery } from "@tanstack/react-query";
import { useId } from "react";
import { Link, useParams, useSearchParams } from "react-router";
import { sitesQuery, snippetQuery } from "@/api/queries";
import { CopyButton } from "@/components/copy-button";
import { ErrorPanel, Loading } from "@/components/error-panel";
import { NativeSelect } from "@/components/form-field";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { isNotFound } from "@/lib/errors";
import { formatBytes, formatDate } from "@/lib/format";
import { snippetGuides } from "@/lib/snippet-guides";
import { NotFoundPage } from "./not-found";
import { useProject } from "./project-layout";

export function SnippetPage() {
  const { tenant } = useProject();
  const { siteId = "" } = useParams();
  const [params, setParams] = useSearchParams();
  const version = params.get("version");
  const sites = useQuery(sitesQuery(tenant.id));
  const snippet = useQuery(snippetQuery(siteId, version));
  const pickerId = useId();

  const site = sites.data?.find((s) => s.id === siteId);
  if (isNotFound(snippet.error)) return <NotFoundPage />;

  return (
    <div className="space-y-6">
      <div>
        <Link
          to={`/projects/${tenant.id}/sites`}
          className="text-sm text-muted-foreground hover:text-foreground"
        >
          Sites
        </Link>
        <h2 className="mt-1 text-xl font-semibold tracking-tight">
          Tracking snippet{site ? ` for ${site.name}` : ""}
        </h2>
        <p className="mt-1 max-w-prose text-muted-foreground">
          One script tag, pinned to a tracker version and locked with an integrity hash, so the code
          that runs on your pages never changes without you changing the tag.
        </p>
      </div>

      {snippet.isPending ? <Loading label="Loading the snippet" /> : null}
      {snippet.error ? (
        <ErrorPanel error={snippet.error} onRetry={() => snippet.refetch()} />
      ) : null}

      {snippet.data ? (
        <>
          <div className="flex flex-wrap items-end gap-4">
            <div className="flex flex-col gap-1.5">
              <label htmlFor={pickerId} className="text-sm font-medium">
                Tracker version
              </label>
              <NativeSelect
                id={pickerId}
                value={snippet.data.version}
                onChange={(e) => {
                  const next = new URLSearchParams(params);
                  next.set("version", e.target.value);
                  setParams(next, { replace: true });
                }}
              >
                {snippet.data.versions.map((ver, i) => (
                  <option key={ver.version} value={ver.version}>
                    {ver.version}
                    {i === 0 ? " (newest)" : ""}
                  </option>
                ))}
              </NativeSelect>
            </div>
            <VersionFacts snippet={snippet.data} />
          </div>

          <Tabs defaultValue="html" className="gap-3">
            <TabsList className="h-auto max-w-full justify-start overflow-x-auto">
              {snippetGuides(snippet.data).map((g) => (
                <TabsTrigger key={g.id} value={g.id}>
                  {g.label}
                </TabsTrigger>
              ))}
            </TabsList>
            {snippetGuides(snippet.data).map((g) => (
              <TabsContent key={g.id} value={g.id} className="space-y-3">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <p className="text-sm">
                    <span className="text-muted-foreground">Where: </span>
                    {g.where}
                  </p>
                  <div className="flex flex-wrap items-center gap-2">
                    <CopyButton
                      text={g.copy}
                      label={g.id === "npm" ? "Copy command" : "Copy tag"}
                    />
                  </div>
                </div>
                <pre className="rounded-lg border bg-canvas p-4 font-mono text-xs leading-relaxed break-all whitespace-pre-wrap sm:text-[0.8rem]">
                  <code>{g.code}</code>
                </pre>
                <p className="max-w-prose text-sm text-muted-foreground">{g.note}</p>
              </TabsContent>
            ))}
          </Tabs>

          <section className="rounded-xl border bg-card p-4 text-sm sm:p-5">
            <h3 className="font-semibold">What the tag pins</h3>
            <dl className="mt-3 grid gap-x-6 gap-y-3 sm:grid-cols-[auto_1fr]">
              <dt className="text-muted-foreground">Script</dt>
              <dd className="break-all font-mono text-xs">{snippet.data.src}</dd>
              <dt className="text-muted-foreground">Integrity</dt>
              <dd className="break-all font-mono text-xs">{snippet.data.integrity}</dd>
              <dt className="text-muted-foreground">Site id</dt>
              <dd className="break-all font-mono text-xs">{snippet.data.site_id}</dd>
            </dl>
            <p className="mt-3 max-w-prose text-muted-foreground">
              The browser refuses the script if a single byte differs from this hash. To move to a
              newer tracker, pick it above and replace the tag on your pages.
            </p>
          </section>
        </>
      ) : null}
    </div>
  );
}

function VersionFacts({
  snippet,
}: {
  snippet: {
    version: string;
    versions: { version: string; released: string; bytes: number; gzip_bytes: number }[];
  };
}) {
  const v = snippet.versions.find((x) => x.version === snippet.version);
  if (!v) return null;
  return (
    <p className="pb-1.5 text-sm text-muted-foreground">
      Released {formatDate(`${v.released}T00:00:00Z`)}, {formatBytes(v.gzip_bytes)} compressed (
      {formatBytes(v.bytes)} raw)
    </p>
  );
}
