// helpers the review queue and the operator project page share: waiting
// times, the enrollment line and the refresh after a decision. the evidence
// list and the decision panel are shared components.

import { cn } from "@veristar/brand/cn";
import { ChevronUp, Clock } from "lucide-react";
import { type ReactNode, useId, useState } from "react";
import { keys } from "@/api/queries";
import type { OperatorTenantDetail } from "@/api/schemas";
import { Button } from "@/components/ui/button";
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

// the box around a decision. on a wide screen it sits in the page after the
// evidence. on a phone it is a slim bar at the bottom of the screen that
// opens to the full panel, so the evidence above it stays readable.
export function DecisionSection({ children }: { children: ReactNode }) {
  const [open, setOpen] = useState(false);
  const id = useId();
  return (
    <section
      aria-labelledby={`${id}-h`}
      data-decision-open={open ? "" : undefined}
      className="flex flex-col gap-3 rounded-lg border border-line bg-raised p-4 max-lg:sticky max-lg:bottom-0 max-lg:z-(--vs-layer-raised) max-lg:-mx-4 max-lg:max-h-[80dvh] max-lg:overflow-y-auto max-lg:rounded-b-none max-lg:bg-overlay max-lg:py-3 max-lg:shadow-overlay sm:p-5"
    >
      <div className="flex items-center justify-between gap-3">
        <h2 id={`${id}-h`} className="text-title text-fg">
          Decision
        </h2>
        <Button
          type="button"
          variant="outline"
          aria-expanded={open}
          aria-controls={`${id}-body`}
          onClick={() => setOpen((o) => !o)}
          className="h-(--vs-control-touch) lg:hidden"
        >
          {open ? "Close" : "Decide"}
          <ChevronUp
            aria-hidden
            className={cn(
              "transition-transform duration-(--vs-duration-change) ease-standard",
              open && "rotate-180",
            )}
          />
        </Button>
      </div>
      <div id={`${id}-body`} className={cn(!open && "max-lg:hidden")}>
        {children}
      </div>
    </section>
  );
}
