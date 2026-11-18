// the tracker snippet for this request, built by the same generator the console
// uses and read from the environment at request time on the server
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
    // SA_SEND_TITLE=true adds data-send-title="true", the console's
    // "send the page title" option
    sendTitle: process.env.SA_SEND_TITLE === "true",
  });
}
