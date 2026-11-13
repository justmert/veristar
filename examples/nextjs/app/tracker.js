// the tracker tag's attributes, read on the server from the environment and
// the release manifest, so tests can use their own site id and gateway. a
// tenant writes the values from the console into app/layout.js instead.
import manifest from "tracker/releases/manifest.json";

function required(name) {
  const value = process.env[name];
  if (!value) throw new Error(`missing env ${name}`);
  return value;
}

export function trackerTag() {
  const version = required("SA_TRACKER_VERSION");
  const entry = manifest.versions.find((r) => r.version === version);
  if (!entry) throw new Error(`tracker version ${version} is not in the release manifest`);
  return {
    src: `${required("SA_GATEWAY_ORIGIN")}/v/${version}/sdk.js`,
    integrity: entry.integrity,
    siteId: required("SA_SITE_ID"),
  };
}
