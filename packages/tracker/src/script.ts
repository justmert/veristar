// entry of the released sdk.js: reads the script tag's data attributes and
// installs window.umami. the attribute names are umami's, so umami's
// integration docs stay valid.

import { type BeforeSend, createTracker, type Payload } from "./index.ts";

((window: Window) => {
  const script = window.document.currentScript as HTMLScriptElement | null;
  if (!script || window.umami) return;

  const attr = (name: string): string | null => script.getAttribute(`data-${name}`);
  const siteId = attr("website-id");
  if (!siteId) return;

  // the gateway is the origin the script was loaded from. a tenant that
  // self-hosts sdk.js sets data-host-url to the gateway origin instead.
  const host = attr("host-url") || new URL(script.src).origin;

  // looked up on every send, as umami does, so the hook may be defined after
  // the script tag
  const hookName = attr("before-send");
  const beforeSend: BeforeSend | undefined = hookName
    ? (type: string, payload: Payload) => {
        const hook = (window as unknown as Record<string, unknown>)[hookName];
        return typeof hook === "function" ? (hook as BeforeSend)(type, payload) : payload;
      }
    : undefined;

  window.umami = createTracker(window, {
    siteId,
    host,
    autoTrack: attr("auto-track") !== "false",
    doNotTrack: attr("do-not-track") === "true",
    excludeSearch: attr("exclude-search") === "true",
    excludeHash: attr("exclude-hash") === "true",
    domains: (attr("domains") || "").split(","),
    beforeSend,
  });
})(window);
