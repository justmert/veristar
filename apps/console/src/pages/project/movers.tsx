import { cn } from "@veristar/brand/cn";
import { ArrowDown, ArrowUp } from "lucide-react";
import type { StatChange } from "@/components/stat-card";

export type Mover = { key: string; label: string; change: StatChange | null };

// what moved against the previous period, largest move first: the line a
// reader takes in before the cards. nothing shows without a previous value.
export function Movers({ items }: { items: Mover[] }) {
  const moved = items
    .filter((m): m is Mover & { change: StatChange } => m.change !== null)
    .filter((m) => m.change.direction !== "flat")
    .sort((x, y) => size(y.change.text) - size(x.change.text))
    .slice(0, 3);
  if (moved.length === 0) return <span />;
  return (
    <ul aria-label="What moved" data-movers="" className="flex flex-wrap gap-x-5 gap-y-1">
      {moved.map((m) => {
        const Icon = m.change.direction === "up" ? ArrowUp : ArrowDown;
        const tone =
          m.change.good === null ? "text-fg" : m.change.good ? "text-success" : "text-critical";
        return (
          <li key={m.key} data-mover={m.key} className="flex items-baseline gap-2 text-body">
            <span className="text-fg-2">{m.label}</span>
            <span className={cn("inline-flex items-center gap-1 font-medium tabular", tone)}>
              <Icon aria-hidden className="size-3.5 self-center" />
              {m.change.text.replace(/^[+-]/, "")}
            </span>
          </li>
        );
      })}
    </ul>
  );
}

// the size of a change from its text ("+50%", "-1.2 pp")
function size(text: string): number {
  const n = Number.parseFloat(text.replace(/[^\d.]/g, ""));
  return Number.isFinite(n) ? n : 0;
}
