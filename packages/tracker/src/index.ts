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
// is used up, and that event is sent once more; from 1.3.1 a tracker loaded
// with auto tracking off sends, reads and fetches nothing, track() included,
// until start(). from 1.3.0 an automatic
// pageview waits until the page is visible and not prerendered, a page
// restored from the back/forward cache counts again, a page hidden from load
// until it is left sends a never-visible notice instead, every event of a
// framed page says so, and screen, language and title are not read unless
// the site asks for the title.

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

export type StartOptions = {
  // false starts only track() and the wallets kit capture, for a site that
  // sends chosen events and no automatic pageviews. default true.
  automatic?: boolean;
};

export type Tracker = {
  track: TrackFunction;
  // starts automatic pageviews, history hooks and click capture. used after
  // consent when the tracker was loaded with auto tracking off; until then
  // track() does nothing either.
  start: (options?: StartOptions) => void;
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
  // send document.title with each event (data-send-title="true"). off by
  // default: no metric needs the title, and a title can name a person.
  sendTitle?: boolean;
};

type TrackerWindow = Window &
  typeof globalThis & {
    doNotTrack?: string | number | null;
    navigator: Navigator & {
      msDoNotTrack?: string | number | null;
      globalPrivacyControl?: boolean;
    };
  };

// a nul character is the one character the gateway refuses in free text
// (postgresql text cannot hold it), and a script can put one into
// document.title. it is dropped so the event is not refused as malformed
const dropNul = (text: string): string => text.split("\u0000").join("");

const DELAY_MS = 300;
// the longest a tracked same-tab link is held for its event when no proof is
// ready yet. a slow or unreachable gateway must not freeze the site's links
const LINK_WAIT_MS = 1000;
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
  const sendTitle = options.sendTitle === true;
  // a page inside a frame can be loaded unseen by an unrelated site, so its
  // events say so and the gateway counts them for the integrity grade
  const framed = top !== win;
  const doc = document as Document & { prerendering?: boolean };

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
  // true once start() hooked the history api, which then keeps currentUrl
  // up to date. before that, and in manual mode, the url is read when needed
  let hooked = false;

  // screen and language stay empty: the gateway and umami take browser, os
  // and device class from the request headers, so nothing more is read from
  // the device. the fields remain so before-send hooks keep their shape.
  const getPayload = (): Payload => {
    if (!hooked) currentUrl = normalize(location.href);
    return {
      website,
      hostname,
      screen: "",
      language: "",
      title: sendTitle ? document.title : "",
      url: currentUrl,
      referrer: stripOrigin(currentRef),
    };
  };

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

  // one challenge and the number of events that used its proof. ready holds
  // the solved proof, so the never-visible notice can be sent synchronously
  // while the page is being left
  type ProofSlot = { proof: Promise<Proof>; fetchedAt: number; uses: number; ready?: Proof };
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
  const usable = (s: ProofSlot | undefined): s is ProofSlot =>
    s !== undefined &&
    win.performance.now() - s.fetchedAt <= CHALLENGE_REFRESH_MS &&
    s.uses < PROOF_MAX_USES;

  const currentProof = (): ProofSlot => {
    if (!usable(slot)) {
      const fresh: ProofSlot = { proof: fetchProof(), fetchedAt: win.performance.now(), uses: 0 };
      fresh.proof.then(
        (p) => {
          fresh.ready = p;
        },
        // a failed challenge is forgotten so the next event asks again
        () => {
          if (slot === fresh) slot = undefined;
        },
      );
      slot = fresh;
    }
    return slot;
  };

  const takeProof = (): ProofSlot => {
    const s = currentProof();
    s.uses++;
    return s;
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
      r: clip(dropNul(payload.referrer || ""), LIMITS.referrer),
      ts: Date.now(),
      p,
    };
    // sent only when set: by data-send-title, or by a before-send hook or
    // track(props) call of the site's own
    if (payload.title) event.ti = clip(dropNul(payload.title), LIMITS.title);
    if (payload.screen) event.s = clip(payload.screen, LIMITS.screen);
    if (payload.language) event.l = clip(payload.language, LIMITS.language);
    if (framed) event.fr = true;
    if (payload.name) {
      // umami truncates long event names; this keeps that documented behaviour
      event.n = clip(payload.name, LIMITS.eventName);
      if (data !== undefined) event.d = data;
    }
    return event;
  };

  // a tracker loaded with auto tracking off waits for start(), which a
  // consent-gated site calls only once the visitor agreed. until then no
  // event goes out, and the opt-out flag is not read from storage either
  const waiting = (): boolean => !autoTrack && !initialized;

  const send = async (input: Payload | null | undefined): Promise<void> => {
    if (waiting() || trackingDisabled()) return;
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
    // checked before the payload is built, so a track(fn) callback does not
    // run either
    if (waiting()) return Promise.resolve();
    try {
      if (typeof name === "string") return send({ ...getPayload(), name, data });
      if (typeof name === "function") return send(name(getPayload()));
      if (typeof name === "object" && name !== null) return send({ ...name });
      return send(getPayload());
    } catch (err) {
      return Promise.reject(err);
    }
  }) as TrackFunction;

  /* visibility: only a page a person could see is counted */

  const canSee = (): boolean => doc.prerendering !== true && doc.visibilityState === "visible";
  // whether the current show of the page (its load, or its restore from the
  // back/forward cache) has been visible
  let seen = false;
  // an automatic pageview waits here while the page is prerendered or hidden.
  // navigations while hidden collapse into one pageview of the page the
  // person finally sees
  let pending = false;

  const pageview = () => {
    if (!canSee()) {
      pending = true;
      return;
    }
    seen = true;
    pending = false;
    void track().catch(() => undefined);
  };

  // visibilitychange and prerenderingchange: the first moment the page can be
  // seen sends the pageview that waited
  const onShown = () => {
    if (!canSee()) return;
    seen = true;
    if (pending) pageview();
  };

  // a page loaded in a background tab gets its proof while hidden, so the
  // never-visible notice can go out when it is closed unseen. a prerendered
  // page fetches nothing before activation.
  const warmProof = () => {
    if (doc.prerendering !== true && !canSee() && !trackingDisabled()) {
      void currentProof().proof.catch(() => undefined);
    }
  };

  // pagehide of a show that was never visible. synchronous, because the page
  // is going away: only a proof that is already solved can be used, so a
  // page closed within moments of loading may send nothing. it carries no
  // url, so no before-send hook needs to see it.
  const neverVisible = () => {
    if (seen || doc.prerendering === true || trackingDisabled()) return;
    const s = slot;
    if (!usable(s) || !s.ready) return;
    s.uses++;
    const event: WireEvent = {
      v: WIRE_VERSION,
      sv: VERSION,
      w: website,
      t: "nv",
      h: clip(hostname, LIMITS.hostname),
      ts: Date.now(),
      p: s.ready,
    };
    if (framed) event.fr = true;
    try {
      win
        .fetch(`${host}${EVENT_PATH}`, {
          method: "POST",
          keepalive: true,
          credentials: "omit",
          headers: { "Content-Type": EVENT_CONTENT_TYPE },
          body: encodeEvent(event),
        })
        .catch(() => undefined);
    } catch {
      // a page that is being torn down may refuse new requests
    }
  };

  const watchVisibility = () => {
    if (canSee()) seen = true;
    document.addEventListener("visibilitychange", onShown);
    document.addEventListener("prerenderingchange", onShown);
    win.addEventListener("pagehide", neverVisible);
    // a restore from the back/forward cache is a new show of the page: it
    // counts again once visible, and can end unseen like a load
    win.addEventListener("pageshow", (e: PageTransitionEvent) => {
      if (!e.persisted) return;
      seen = false;
      pageview();
      warmProof();
    });
  };

  /* navigation */

  // a call for the url already current changes nothing. next.js calls
  // replaceState with the current url after it handled a popstate; moving the
  // referrer there made the pageview of a back or forward navigation name the
  // page itself as its referrer.
  //
  // a navigation within the delay of the previous one, such as a route that
  // redirects at once, joins it: one pageview of where the visitor lands,
  // with the referrer of the first, and none when that is the counted page.
  let navigating = false;
  const onNavigate = (url: string | URL | null | undefined) => {
    if (!url) return;
    const next = normalize(url);
    if (next === currentUrl) return;
    if (!navigating) currentRef = currentUrl;
    currentUrl = next;
    if (navigating) return;
    navigating = true;
    setTimeout(() => {
      navigating = false;
      if (currentUrl !== currentRef) pageview();
    }, DELAY_MS);
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
    // the page may have moved on since the tracker loaded (consent given
    // after spa navigation); from here the hooks follow it
    currentUrl = normalize(location.href);
    hooked = true;
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

    // whether the browser itself would load href in this page or its top or
    // parent window, which ends this page. a link into another window, a
    // download or a modified click leaves the page running
    const leavesPage = (el: HTMLAnchorElement, e: MouseEvent): boolean => {
      const linkTarget = el.target.toLowerCase();
      return (
        !el.hasAttribute("download") &&
        ["", "_self", "_top", "_parent"].includes(linkTarget) &&
        !(e.ctrlKey || e.shiftKey || e.metaKey || e.altKey || e.button === 1)
      );
    };

    // a proof already solved goes out with the event in this same turn, as a
    // keepalive request that outlives the page, so the click needs no hold
    const proofReady = (): boolean => usable(slot) && slot.ready !== undefined;

    // the link click being dispatched, until the window sees it bubble up
    let clicked: { e: MouseEvent; el: HTMLAnchorElement; sent: Promise<void> } | undefined;

    document.addEventListener(
      "click",
      (e: MouseEvent) => {
        clicked = undefined;
        const target = e.target as Element | null;
        if (!target || typeof target.closest !== "function") return;
        const el = target.closest(`[${EVENT_ATTRIBUTE}]`);
        if (!el?.getAttribute(EVENT_ATTRIBUTE)) return;
        const sent = trackElement(el).catch(() => undefined);
        if (el.tagName === "A" && (el as HTMLAnchorElement).href) {
          const a = el as HTMLAnchorElement;
          if (leavesPage(a, e) && !proofReady()) clicked = { e, el: a, sent };
        }
      },
      true,
    );

    // runs after the page's own click handlers. a client-side router that
    // handles the link has prevented the default by now and keeps the page,
    // so the event goes out without help. otherwise the browser would end the
    // page before the proof is solved: the navigation waits for the event, at
    // most LINK_WAIT_MS, and is then made by the tracker
    win.addEventListener("click", (e: MouseEvent) => {
      const c = clicked;
      clicked = undefined;
      if (!c || c.e !== e || e.defaultPrevented) return;
      e.preventDefault();
      const { href } = c.el;
      const linkTarget = c.el.target.toLowerCase();
      let done = false;
      const go = () => {
        if (done) return;
        done = true;
        const frame = linkTarget === "_top" ? top : linkTarget === "_parent" ? win.parent : null;
        (frame ?? win).location.href = href;
      };
      void c.sent.then(go);
      setTimeout(go, LINK_WAIT_MS);
    });
  };

  const start = (startOptions?: StartOptions) => {
    if (initialized) return;
    initialized = true;
    if (startOptions?.automatic !== false) {
      watchVisibility();
      pageview();
      warmProof();
      hookHistory();
      handleClicks();
    }
    // started with tracking, so a consent-gated site does not have the kit's
    // storage read before start(), and checked here because start() can be
    // called for a visitor with gpc, an opt-out or do-not-track honoured:
    // their storage is never read
    if (options.walletsKit === true && !trackingDisabled()) watchWalletsKitStorage(win, track);
  };

  if (autoTrack && !trackingDisabled()) {
    if (document.readyState === "complete") start();
    else document.addEventListener("readystatechange", () => start(), true);
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
