import { Info } from "lucide-react";
import { Button } from "@/components/ui/button";
import { leaveDemo } from "@/lib/demo";

// the one line every demo page carries, as small as a toast and where none
// sits: toasts take the right corner (the top on a phone), and a list's
// selection bar the middle of a wide screen. the enroll link
// is a full page load: the tab leaves the demo first, so sign-in starts
// from an empty cache and the real api
export function DemoBanner() {
  return (
    <div
      role="status"
      data-demo-banner=""
      className="pointer-events-none fixed inset-x-0 bottom-0 z-(--vs-layer-toast) flex justify-center p-4 lg:left-(--vs-sidebar) lg:justify-start"
    >
      <p className="pointer-events-auto flex animate-rise-in items-center gap-2.5 rounded-[10px] border border-line-2 bg-overlay px-3.5 py-2 text-small text-fg shadow-overlay">
        <Info aria-hidden className="size-4 shrink-0 text-info" />
        <span>Demo with sample data. Read-only.</span>
        <Button asChild variant="link" size="sm" className="shrink-0">
          <a href="/sign-in" onClick={() => leaveDemo()}>
            Enroll your project
          </a>
        </Button>
      </p>
    </div>
  );
}
