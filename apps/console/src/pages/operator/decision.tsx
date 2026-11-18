// helpers the review queue and the operator project page share: waiting
// times, the enrollment line and the refresh after a decision. the evidence
// list and the decision panel are shared components.

import { Clock } from "lucide-react";
import { keys } from "@/api/queries";
import type { OperatorTenantDetail } from "@/api/schemas";
import { formatDate } from "@/lib/format";
import { queryClient } from "@/query-client";

export async function refreshOperator() {
  await queryClient.invalidateQueries({ queryKey: keys.operator });
}

// "today", "5 h", "3 d": how long something has waited, for the queue
export function ageText(iso: string, now: number): string {
  const h = Math.floor((now - new Date(iso).getTime()) / 3_600_000);
  if (h < 1) return "now";
  if (h < 24) return `${h} h`;
  return `${Math.floor(h / 24)} d`;
}

export function waitingText(iso: string, now: number): string {
  const d = Math.floor((now - new Date(iso).getTime()) / 86_400_000);
  if (d < 1) return "Waiting since today";
  return d === 1 ? "Waiting 1 day" : `Waiting ${d} days`;
}

export function Waiting({ since, now }: { since: string; now: number }) {
  return (
    <span className="flex items-center gap-1 text-caption text-warning">
      <Clock aria-hidden className="size-3" strokeWidth={2.4} />
      {waitingText(since, now)}
    </span>
  );
}

export function enrolledLine(t: OperatorTenantDetail["tenant"]): string {
  return `Enrolled ${formatDate(t.created_at)}${t.created_by ? ` by ${t.created_by.display_name}` : ""}.`;
}
