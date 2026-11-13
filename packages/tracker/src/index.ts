/*!
 * stellar analytics tracker, derived from the umami tracker v3.5.0
 * (src/tracker/index.ts, https://github.com/umami-software/umami).
 * copyright (c) 2022 umami software, inc. <hello@umami.is>
 * copyright (c) 2026 the stellar analytics contributors (changes)
 * released under the mit license, see packages/tracker/LICENSE.
 */

// changes from umami: events use wire format v1 and carry a proof of work
// solved against a gateway challenge; identify, distinct ids, client salts,
// web vitals, error capture, the credentials option and recorder hooks are
// removed; popstate is tracked; global privacy control stops all sending;
// tracking can be started later (after consent) with start(); a proof is
// replaced before the gateway's per-proof limit and after a 403 that says it
// is used up, and that event is sent once more.

import { solve } from "./proof.ts";
import { watchWalletsKitStorage } from "./wallets-kit.ts";
import {
  byteLength,
  CHALLENGE_PATH,
  CHALLENGE_REFRESH_MS,
  type Challenge,
  checkData,
  checkName,
  clip,
  EVENT_CONTENT_TYPE,
  EVENT_PATH,
  encodeEvent,
  LIMITS,
  PROOF_MAX_USES,
  type Proof,
  parseChallenge,
  routeFragment,
  STALE_PROOF_REASONS,
  WIRE_VERSION,
  type WireData,
  type WireEvent,
} from "./wire.ts";

export { leadingZeroBits, solve, verifyNonce } from "./proof.ts";
export {
  type AttachOptions,
  attachWalletsKit,
  networkName,
  type WalletNetwork,
  type WalletsKitLike,
} from "./wallets-kit.ts";
export * from "./wire.ts";

declare const __SDK_VERSION__: string;
export const VERSION: string = __SDK_VERSION__;

export type EventDataValue = string | number | boolean;
export type EventData = Record<string, EventDataValue>;

// umami-shaped payload, so data-before-send and track(fn) callbacks written for
// umami keep working. it is turned into a wire event after the hook ran.
export type Payload = {
  website: string;
  hostname: string;
  screen: string;
  language: string;
  title: string;
  url: string;
  referrer: string;
  name?: string;
  data?: EventData;
};

export type BeforeSend = (
  type: string,
  payload: Payload,
) => Payload | null | undefined | Promise<Payload | null | undefined>;

export type TrackFunction = {
  (): Promise<void>;
  (eventName: string, data?: EventData): Promise<void>;
  (properties: Payload): Promise<void>;
  (fn: (props: Payload) => Payload): Promise<void>;
};

export type Tracker = {
  track: TrackFunction;
  // starts automatic pageviews, history hooks and click capture. used after
  // consent when the tracker was loaded with auto tracking off.
  start: () => void;
  version: string;
};

export type TrackerOptions = {
  siteId: string;
  // gateway origin, for example https://in.<BASE_DOMAIN>
  host: string;
  autoTrack?: boolean;
  doNotTrack?: boolean;
  excludeSearch?: boolean;
  excludeHash?: boolean;
  domains?: string[];
  beforeSend?: BeforeSend;
  // watch the stellar wallets kit's localStorage keys and send wallet events
  // (data-wallets-kit="true"). bundler users call attachWalletsKit instead.
  walletsKit?: boolean;
};

type TrackerWindow = Window &
  typeof globalThis & {
    doNotTrack?: string | number | null;
    navigator: Navigator & {
      msDoNotTrack?: string | number | null;
      globalPrivacyControl?: boolean;
    };
  };

const DELAY_MS = 300;
const EVENT_ATTRIBUTE = "data-umami-event";
const EVENT_DATA_ATTRIBUTE = /^data-umami-event-([\w-]+)$/;

export function createTracker(window: Window, options: TrackerOptions): Tracker {
  const win = window as TrackerWindow;
  const { location, document, history, top, navigator } = win;
  const { hostname, origin } = location;
  const website = options.siteId;
  const host = options.host.replace(/\/$/, "");
  const autoTrack = options.autoTrack !== false;
  const excludeSearch = options.excludeSearch === true;
  const excludeHash = options.excludeHash === true;
  const domains = (options.domains ?? []).map((d) => d.trim()).filter((d) => d !== "");
  const screen = `${win.screen.width}x${win.screen.height}`;

  let storage: Storage | undefined;
  try {
    storage = location.href.startsWith("data:") ? undefined : win.localStorage;
  } catch {
    // a sandboxed or storage-blocked document throws on access. the opt-out
    // flag cannot be set there either, so there is nothing to read.
  }

  const normalize = (raw: string | URL): string => {
    const u = new URL(raw, location.href);
    if (excludeSearch) u.search = "";
    u.hash = routeFragment(u.hash, excludeHash);
    return u.toString();
  };

  // same-origin referrers are kept as a path so the site's own domain is not
  // recorded as a referrer, as umami does
  const stripOrigin = (url: string): string =>
    url === origin || url.startsWith(`${origin}/`) ? url.slice(origin.length) : url;

  let currentUrl = normalize(location.href);
  let currentRef = document.referrer ? normalize(document.referrer) : "";
  let initialized = false;

  const getPayload = (): Payload => ({
    website,
    hostname,
    screen,
    language: navigator.language,
    title: document.title,
    url: currentUrl,
    referrer: stripOrigin(currentRef),
  });

  const hasDoNotTrack = () => {
    const dnt = win.doNotTrack || navigator.doNotTrack || navigator.msDoNotTrack;
    return dnt === 1 || dnt === "1" || dnt === "yes";
  };

  const trackingDisabled = (): boolean =>
    !website ||
    navigator.globalPrivacyControl === true ||
    Boolean(storage?.getItem("umami.disabled")) ||
    (win.name === "umami.disabled" && top !== win) ||
    (domains.length > 0 && !domains.includes(hostname)) ||
    (options.doNotTrack === true && hasDoNotTrack());

  /* challenge and proof, held in memory only */

  // one challenge and the number of events that used its proof
  type ProofSlot = { proof: Promise<Proof>; fetchedAt: number; uses: number };
  let slot: ProofSlot | undefined;

  const fetchProof = async (): Promise<Proof> => {
    const res = await win.fetch(`${host}${CHALLENGE_PATH}?w=${encodeURIComponent(website)}`, {
      method: "GET",
      credentials: "omit",
      cache: "no-store",
    });
    if (!res.ok) throw new Error(`challenge request failed with ${res.status}`);
    const challenge: Challenge | string = parseChallenge(await res.json(), website);
    if (typeof challenge === "string") throw new Error(challenge);
    return solve(win.crypto.subtle, challenge);
  };

  // takes one use of the current proof. a spa keeps one page load for as
  // long as the tab is open, so the proof is replaced after PROOF_MAX_USES
  // events, before the gateway's per-proof limit, as well as after 5 hours
  const takeProof = (): ProofSlot => {
    const now = win.performance.now();
    if (!slot || now - slot.fetchedAt > CHALLENGE_REFRESH_MS || slot.uses >= PROOF_MAX_USES) {
      const fresh: ProofSlot = { proof: fetchProof(), fetchedAt: now, uses: 0 };
      // a failed challenge is forgotten so the next event asks again
      fresh.proof.catch(() => {
        if (slot === fresh) slot = undefined;
      });
      slot = fresh;
    }
    slot.uses++;
    return slot;
  };

  // forgets a proof the gateway called used up, unless another event has
  // already replaced it
  const dropProof = (used: ProofSlot) => {
    if (slot === used) slot = undefined;
  };

  /* wire conversion */

  const toWire = (payload: Payload, data: WireData | undefined, p: Proof): WireEvent => {
    const u = new URL(payload.url, location.href);
    const event: WireEvent = {
      v: WIRE_VERSION,
      sv: VERSION,
      w: website,
      t: payload.name ? "ev" : "pv",
      h: clip(payload.hostname || u.hostname, LIMITS.hostname),
      // applied again because before-send and track(props) can supply any url
      u: clip(`${u.pathname}${u.search}${routeFragment(u.hash, excludeHash)}`, LIMITS.url),
      r: clip(payload.referrer || "", LIMITS.referrer),
      ti: clip(payload.title || "", LIMITS.title),
      s: clip(payload.screen || "", LIMITS.screen),
      l: clip(payload.language || "", LIMITS.language),
      ts: Date.now(),
      p,
    };
    if (payload.name) {
      // umami truncates long event names; this keeps that documented behaviour
      event.n = clip(payload.name, LIMITS.eventName);
      if (data !== undefined) event.d = data;
    }
    return event;
  };

  const send = async (input: Payload | null | undefined): Promise<void> => {
    if (trackingDisabled()) return;
    let payload = input;
    if (options.beforeSend) payload = await options.beforeSend("event", payload as Payload);
    if (!payload) return;

    // validation runs before the network so a bad event fails fast and loudly
    let data: WireData | undefined;
    if (payload.name) {
      const bad = checkName(payload.name);
      if (bad) throw new TypeError(`umami: ${bad}`);
    }
    if (payload.name && payload.data !== undefined) {
      const checked = checkData(payload.data);
      if (typeof checked === "string") throw new TypeError(`umami: ${checked}`);
      data = checked;
    }

    // the second round runs only when the gateway called the proof used up
    for (let round = 0; round < 2; round++) {
      const used = takeProof();
      let p: Proof;
      try {
        p = await used.proof;
      } catch {
        // the gateway is unreachable or blocked; the page must not break
        // because of that, and there is no other place to send the event
        return;
      }
      const body = encodeEvent(toWire(payload, data, p));
      if (byteLength(body) > LIMITS.bodyBytes) {
        throw new TypeError(`umami: event body exceeds ${LIMITS.bodyBytes} bytes`);
      }
      let res: Response;
      try {
        res = await win.fetch(`${host}${EVENT_PATH}`, {
          method: "POST",
          keepalive: true,
          credentials: "omit",
          headers: { "Content-Type": EVENT_CONTENT_TYPE },
          body,
        });
      } catch {
        // same reason as above: a network failure is not the page's problem
        return;
      }
      if (res.status !== 403) return;
      let reason = "";
      try {
        reason = (await res.text()).trim();
      } catch {
        return;
      }
      if (!STALE_PROOF_REASONS.includes(reason)) return;
      dropProof(used);
    }
  };

  const track = ((
    name?: string | Payload | ((props: Payload) => Payload),
    data?: EventData,
  ): Promise<void> => {
    try {
      if (typeof name === "string") return send({ ...getPayload(), name, data });
      if (typeof name === "function") return send(name(getPayload()));
      if (typeof name === "object" && name !== null) return send({ ...name });
      return send(getPayload());
    } catch (err) {
      return Promise.reject(err);
    }
  }) as TrackFunction;

  /* navigation */

  // a call for the url already current changes nothing. next.js calls
  // replaceState with the current url after it handled a popstate; moving the
  // referrer there made the pageview of a back or forward navigation name the
  // page itself as its referrer.
  const onNavigate = (url: string | URL | null | undefined) => {
    if (!url) return;
    const next = normalize(url);
    if (next === currentUrl) return;
    currentRef = currentUrl;
    currentUrl = next;
    setTimeout(() => void track(), DELAY_MS);
  };

  const hookHistory = () => {
    const hook = (method: "pushState" | "replaceState") => {
      const orig = history[method];
      history[method] = (...args: Parameters<History["pushState"]>) => {
        const result = orig.apply(history, args);
        onNavigate(args[2]);
        return result;
      };
    };
    hook("pushState");
    hook("replaceState");
    // umami has no popstate listener, so back and forward in an spa were not
    // counted. popstate also fires for fragment navigation; only "#/" route
    // fragments are kept by normalize, so a jump to an anchor is no pageview.
    win.addEventListener("popstate", () => onNavigate(location.href));
  };

  const handleClicks = () => {
    const trackElement = (el: Element): Promise<void> => {
      const eventName = el.getAttribute(EVENT_ATTRIBUTE) as string;
      const eventData: EventData = {};
      for (const attr of el.getAttributeNames()) {
        const match = attr.match(EVENT_DATA_ATTRIBUTE);
        if (match?.[1]) eventData[match[1]] = el.getAttribute(attr) as string;
      }
      return track(eventName, eventData);
    };

    document.addEventListener(
      "click",
      (e: MouseEvent) => {
        const target = e.target as Element | null;
        if (!target || typeof target.closest !== "function") return;
        const el = target.closest(`[${EVENT_ATTRIBUTE}]`);
        if (!el?.getAttribute(EVENT_ATTRIBUTE)) return;

        if (el.tagName === "A" && (el as HTMLAnchorElement).href) {
          const { href, target: linkTarget } = el as HTMLAnchorElement;
          const external =
            linkTarget === "_blank" || e.ctrlKey || e.shiftKey || e.metaKey || e.button === 1;
          // same-tab links wait for the event so the navigation does not cancel it
          if (!external) e.preventDefault();
          void trackElement(el)
            .catch(() => undefined)
            .finally(() => {
              if (!external) {
                (linkTarget === "_top" && top ? top.location : location).href = href;
              }
            });
          return;
        }
        void trackElement(el).catch(() => undefined);
      },
      true,
    );
  };

  const start = () => {
    if (initialized) return;
    initialized = true;
    void track().catch(() => undefined);
    hookHistory();
    handleClicks();
    // started with tracking, so a consent-gated site does not have the kit's
    // storage read before start(), and checked here because start() can be
    // called for a visitor with gpc, an opt-out or do-not-track honoured:
    // their storage is never read
    if (options.walletsKit === true && !trackingDisabled()) watchWalletsKitStorage(win, track);
  };

  if (autoTrack && !trackingDisabled()) {
    if (document.readyState === "complete") start();
    else document.addEventListener("readystatechange", start, true);
  }

  return { track, start, version: VERSION };
}

declare global {
  interface Window {
    umami?: Tracker;
  }
}

// esm entry for bundler users. the tracker is also installed as window.umami
// when no other copy is, so umami.track() and data-umami-event work the same
// as with the script tag.
export function init(options: TrackerOptions): Tracker {
  if (typeof window === "undefined") throw new Error("umami: init() needs a browser window");
  if (!options.siteId) throw new Error("umami: siteId is required");
  if (!options.host) throw new Error("umami: host is required");
  if (window.umami) return window.umami;
  const tracker = createTracker(window, options);
  window.umami = tracker;
  return tracker;
}
