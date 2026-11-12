// db-ip's lite licence (cc by 4.0) makes a link back a condition of use on
// every page that displays or uses its results (docs/reference/the reference docs
// section 5). pages with country data, and pages that open the web
// dashboards, which show countries, render this.
export function DbIpAttribution({ className }: { className?: string }) {
  return (
    <p className={`text-xs text-muted-foreground ${className ?? ""}`}>
      Country data:{" "}
      <a
        href="https://db-ip.com"
        className="underline underline-offset-2 hover:text-foreground"
        rel="noopener"
      >
        IP Geolocation by DB-IP
      </a>
    </p>
  );
}
