import { ChevronRight } from "lucide-react";
import { Link } from "react-router";
import type { OperatorTenantSummary } from "@/api/schemas";
import { StatusPill } from "@/components/status";
import { formatDate } from "@/lib/format";

// one row per project, used by the review queue and the project list
export function TenantList({ items }: { items: OperatorTenantSummary[] }) {
  return (
    <ul className="divide-y overflow-hidden rounded-xl border bg-card">
      {items.map((t) => (
        <li key={t.id}>
          <Link
            to={`/operator/tenants/${t.id}`}
            className="group flex items-center gap-4 px-4 py-4 hover:bg-muted/60 focus-visible:bg-muted/60 sm:px-5"
          >
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-2">
                <p className="truncate font-medium">{t.name}</p>
                <StatusPill status={t.status} />
              </div>
              <p className="truncate text-sm text-muted-foreground">
                {t.slug}, {t.website}
              </p>
              <p className="mt-1 text-xs text-muted-foreground">
                {t.site_count} {t.site_count === 1 ? "site" : "sites"}, {t.sites_verified} verified,{" "}
                {t.sites_with_stellar_toml} with stellar.toml
                {t.sites_in_review > 0 ? `, ${t.sites_in_review} in review` : ""}. Enrolled{" "}
                {formatDate(t.created_at)}, status since {formatDate(t.status_changed_at)}.
              </p>
              {t.status_reason ? (
                <p className="mt-1 truncate text-xs text-muted-foreground">
                  Reason: {t.status_reason}
                </p>
              ) : null}
            </div>
            <ChevronRight
              aria-hidden
              className="size-4 text-muted-foreground transition-transform group-hover:translate-x-0.5"
            />
          </Link>
        </li>
      ))}
    </ul>
  );
}
