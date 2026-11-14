// a sibling service of the console's app.<BASE_DOMAIN>: docs.<BASE_DOMAIN>,
// status.<BASE_DOMAIN>. null when the console runs on another host name (a
// local dev server), so no link points at a guessed address.
export function siblingUrl(hostname: string, name: "docs" | "status"): string | null {
  return hostname.startsWith("app.") && hostname.length > 4
    ? `https://${name}.${hostname.slice(4)}`
    : null;
}
