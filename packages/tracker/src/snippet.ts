// builds the script tag tenants paste into their pages. the control plane
// builds the same bytes in go; this stage tests the two for byte equality, so the
// attribute order and quoting here are part of the contract.

import manifest from "../releases/manifest.json" with { type: "json" };
import revoked from "../releases/revoked.json" with { type: "json" };

export type ReleaseEntry = {
  version: string;
  file: string;
  bytes: number;
  gzipBytes: number;
  sha384: string;
  integrity: string;
};

export type SnippetOptions = {
  siteId: string;
  version: string;
  // gateway origin, for example https://in.<BASE_DOMAIN>, without a trailing slash
  origin: string;
};

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

export function releases(): ReleaseEntry[] {
  return (manifest as { versions: ReleaseEntry[] }).versions;
}

export function isRevoked(version: string): boolean {
  return (revoked as { revoked: { version: string }[] }).revoked.some((r) => r.version === version);
}

export function scriptUrl(origin: string, version: string): string {
  return `${origin}/v/${version}/sdk.js`;
}

export function snippet({ siteId, version, origin }: SnippetOptions): string {
  // values are checked instead of html-escaped: every accepted value consists
  // of characters that need no escaping inside a double-quoted attribute
  if (!UUID.test(siteId)) throw new Error(`site id ${siteId} is not a lowercase uuid`);
  let parsed: URL;
  try {
    parsed = new URL(origin);
  } catch {
    throw new Error(`origin ${origin} is not a url`);
  }
  if (parsed.origin !== origin || (parsed.protocol !== "https:" && parsed.protocol !== "http:"))
    throw new Error(`origin ${origin} must be a bare http or https origin`);
  const entry = releases().find((r) => r.version === version);
  if (!entry) throw new Error(`tracker version ${version} is not in the release manifest`);
  if (isRevoked(version)) throw new Error(`tracker version ${version} is revoked`);
  return `<script defer src="${scriptUrl(origin, version)}" integrity="${entry.integrity}" crossorigin="anonymous" data-website-id="${siteId}"></script>`;
}
