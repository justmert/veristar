import { cn } from "@veristar/brand/cn";

// db-ip's lite licence (cc by 4.0) makes a link back a condition of use on
// every page that displays or uses its results (docs/reference/geo.md, licence
// and attribution). pages with country data, and pages that open the web
// dashboards, which show countries, render this.
export function DbIpAttribution({ className }: { className?: string }) {
  return (
    <p className={cn("text-caption font-normal text-fg-3", className)}>
      Country data:{" "}
      <a
        href="https://db-ip.com"
        className="rounded-sm underline decoration-line-strong underline-offset-2 transition-colors duration-(--vs-duration-press) hover:text-fg hover:decoration-fg focus-ring"
        rel="noopener"
      >
        IP Geolocation by DB-IP
      </a>
    </p>
  );
}
