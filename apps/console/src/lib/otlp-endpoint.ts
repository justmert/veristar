// otlp.<BASE_DOMAIN> next to the console's app.<BASE_DOMAIN>, the host layout
// every deployment uses.
// null when the console is served from another host name, because the
// endpoint cannot be derived then and guessing one would mislead.
export function otlpEndpoint(hostname: string): string | null {
  return hostname.startsWith("app.") && hostname.length > 4
    ? `https://otlp.${hostname.slice(4)}`
    : null;
}
