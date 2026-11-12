// builds the tracker tag from the environment with the same generator the
// console uses, so the page carries the exact snippet a tenant would paste.
import { snippet } from "tracker/snippet";

function required(name: string): string {
  const value = process.env[name];
  if (!value) throw new Error(`missing env ${name}`);
  return value;
}

export function trackerTag(): string {
  return snippet({
    siteId: required("SA_SITE_ID"),
    version: required("SA_TRACKER_VERSION"),
    origin: required("SA_GATEWAY_ORIGIN"),
  });
}
