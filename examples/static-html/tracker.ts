// builds the tracker tags from the environment with the same generator the
// console uses, so the pages carry the exact snippet a tenant would paste.
import { releases, snippet } from "tracker/snippet";

function required(name: string): string {
  const value = process.env[name];
  if (!value) throw new Error(`missing env ${name}`);
  return value;
}

// the tag as the console shows it
export function trackerTag(): string {
  return snippet({
    siteId: required("SA_SITE_ID"),
    version: required("SA_TRACKER_VERSION"),
    origin: required("SA_GATEWAY_ORIGIN"),
  });
}

// the console's tag with one attribute added before the closing bracket, the
// change the consent and self-hosting guides tell a tenant to make by hand
function withAttribute(tag: string, attribute: string): string {
  if (!tag.endsWith("></script>")) throw new Error("unexpected tag shape");
  return `${tag.slice(0, -"></script>".length)} ${attribute}></script>`;
}

// deferred start: nothing is sent until the page calls umami.start()
export function consentTag(): string {
  return withAttribute(trackerTag(), 'data-auto-track="false"');
}

// path under dist/ where the build copies the release's sdk.js, for the
// self-hosting page
export function selfHostedPath(): string {
  return `/sdk/${required("SA_TRACKER_VERSION")}/sdk.js`;
}

// the same file served from the site's own origin: src points there, the
// integrity value stays the release's, and data-host-url names the gateway
// because sdk.js can no longer derive it from its own address
export function selfHostedTag(): string {
  const version = required("SA_TRACKER_VERSION");
  const gateway = required("SA_GATEWAY_ORIGIN");
  const tag = trackerTag();
  const src = `src="${gateway}/v/${version}/sdk.js"`;
  if (!tag.includes(src)) throw new Error("unexpected tag shape");
  return withAttribute(tag.replace(src, `src="${selfHostedPath()}"`), `data-host-url="${gateway}"`);
}

export function releaseFile(): string {
  const version = required("SA_TRACKER_VERSION");
  const entry = releases().find((r) => r.version === version);
  if (!entry) throw new Error(`tracker version ${version} is not in the release manifest`);
  return entry.file;
}
