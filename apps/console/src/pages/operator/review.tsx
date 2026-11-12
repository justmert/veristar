import { useInfiniteQuery } from "@tanstack/react-query";
import type { TenantListQuery } from "@/api/endpoints";
import { operatorTenantsQuery } from "@/api/queries";
import { ErrorPanel, Loading } from "@/components/error-panel";
import { Button } from "@/components/ui/button";
import { TenantList } from "./tenant-table";

// contract 5.3 review=true: pending projects and active projects with a site
// in review, oldest first so nobody waits longest
const QUEUE: TenantListQuery = {
  status: [],
  review: true,
  q: "",
  sort: "created_at",
  order: "asc",
};

export function ReviewQueuePage() {
  const list = useInfiniteQuery(operatorTenantsQuery(QUEUE));
  if (list.isPending) return <Loading label="Loading the review queue" />;
  if (list.error) return <ErrorPanel error={list.error} onRetry={() => list.refetch()} />;
  const items = list.data.pages.flatMap((p) => p.items);

  return (
    <section className="space-y-4">
      <div>
        <h2 className="text-xl font-semibold tracking-tight">Review queue</h2>
        <p className="mt-1 text-sm text-muted-foreground">
          Projects waiting for approval, and active projects with a site whose domain belonged to a
          removed project. Oldest first.
        </p>
      </div>
      {items.length === 0 ? (
        <p className="rounded-xl border border-dashed bg-card p-6 text-muted-foreground">
          Nothing to review.
        </p>
      ) : (
        <TenantList items={items} />
      )}
      {list.hasNextPage ? (
        <Button
          variant="outline"
          onClick={() => list.fetchNextPage()}
          disabled={list.isFetchingNextPage}
        >
          {list.isFetchingNextPage ? "Loading" : "Load more"}
        </Button>
      ) : null}
    </section>
  );
}
