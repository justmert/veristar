// the tracker tag's attributes, read on the server for every request. they come
// from the same release manifest the snippet generator uses; react renders them
// as a script element, which is how a react router app carries the snippet.
import { releases, scriptUrl } from "tracker/snippet";

export type TrackerTag = { src: string; integrity: string; siteId: string };

function required(name: string): string {
  const value = process.env[name];
  if (!value) throw new Error(`missing env ${name}`);
  return value;
}

export function trackerTag(): TrackerTag {
  const version = required("SA_TRACKER_VERSION");
  const entry = releases().find((r) => r.version === version);
  if (!entry) throw new Error(`tracker version ${version} is not in the release manifest`);
  return {
    src: scriptUrl(required("SA_GATEWAY_ORIGIN"), version),
    integrity: entry.integrity,
    siteId: required("SA_SITE_ID"),
  };
}
