// puts the generated tracker snippet where app.html has <!-- tracker -->, so
// every server-rendered page carries the exact tag a tenant would paste. a
// tenant pastes the tag from the console into src/app.html instead; this hook
// only exists so the test site id and gateway origin can come from the
// environment at run time.
import type { Handle } from "@sveltejs/kit";
import { snippet } from "tracker/snippet";

function required(name: string): string {
  const value = process.env[name];
  if (!value) throw new Error(`missing env ${name}`);
  return value;
}

export const handle: Handle = ({ event, resolve }) =>
  resolve(event, {
    transformPageChunk: ({ html }) => {
      if (!html.includes("<!-- tracker -->")) return html;
      const tag = snippet({
        siteId: required("SA_SITE_ID"),
        version: required("SA_TRACKER_VERSION"),
        origin: required("SA_GATEWAY_ORIGIN"),
      });
      return html.replace("<!-- tracker -->", tag);
    },
  });
