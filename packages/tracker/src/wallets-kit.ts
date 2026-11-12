// stellar wallets kit capture (the design). two ways in, both opt-in:
//
//   attachWalletsKit(StellarWalletsKit) for bundler users: subscribes to the
//   kit's own events (STATE_UPDATE, WALLET_SELECTED, DISCONNECT).
//
//   data-wallets-kit="true" on the script tag: the kit ships esm and cjs only
//   and sets no browser global, so a separately loaded sdk.js cannot reach
//   its events. it watches the kit's same-origin localStorage keys instead,
//   for presence and for the wallet id only.
//
// capture is opt-in on purpose. reading another library's browser storage on
// every site by default is access to stored information (eprivacy art. 5(3))
// and would undermine running without a consent banner, so the tenant turns
// it on for its own site.
//
// the account address is never sent, hashed or stored. the adapter only
// tests whether an address exists, to notice the change from no address to
// an address; the storage watcher never reads the address key's value at all.

import type { EventData } from "./index.ts";

export const WALLET_SELECTED = "wallet_selected";
export const WALLET_CONNECTED = "wallet_connected";
export const WALLET_DISCONNECTED = "wallet_disconnected";

// the kit's KitEventType values, identical in 2.7.1 and 2.8.0. literal strings
// keep the tracker free of a dependency on the kit.
const STATE_UPDATE = "STATE_UPDATE";
const WALLET_SELECTED_EVENT = "WALLET_SELECTED";
const DISCONNECT = "DISCONNECT";

// the kit's LocalStorageKeys, identical in 2.7.1 and 2.8.0
export const KIT_SELECTED_KEY = "@StellarWalletsKit/selectedModuleId";
export const KIT_ADDRESS_KEY = "@StellarWalletsKit/activeAddress";

// how often the storage watcher looks. the kit writes its keys from inside
// the page, which raises no storage event in the same document.
export const STORAGE_POLL_MS = 500;

// network passphrases of stellar (sep-0029 values the kit's Networks enum uses)
const PASSPHRASES: Record<string, string> = {
  "Public Global Stellar Network ; September 2015": "public",
  "Test SDF Network ; September 2015": "testnet",
  "Test SDF Future Network ; October 2022": "futurenet",
};

export type WalletNetwork = "public" | "testnet" | "futurenet" | "other";

// anything that is not one of the three shared networks (sandbox,
// standalone, a private network) is "other": a private passphrase can name
// an organisation, so it is never sent as text
export function networkName(passphrase: string | undefined): WalletNetwork {
  return (passphrase && (PASSPHRASES[passphrase] as WalletNetwork | undefined)) || "other";
}

export type KitStateEvent = {
  eventType: string;
  payload: { address?: string | undefined; networkPassphrase?: string };
};
export type KitWalletEvent = { eventType: string; payload: { id?: string | undefined } };
export type KitDisconnectEvent = { eventType: string; payload: Record<PropertyKey, never> };

// the part of the kit's static class this module uses. written as method
// overloads so the kit's own class, whose parameter is the KitEventType enum,
// is accepted as it is.
export interface WalletsKitLike {
  on(type: "STATE_UPDATE", callback: (event: KitStateEvent) => void): () => void;
  on(type: "WALLET_SELECTED", callback: (event: KitWalletEvent) => void): () => void;
  on(type: "DISCONNECT", callback: (event: KitDisconnectEvent) => void): () => void;
}

// what the adapter needs from a tracker: umami.track(name, data)
export type TrackLike = { track: (name: string, data?: EventData) => Promise<void> };

export type AttachOptions = {
  // defaults to window.umami, which init() and sdk.js install
  tracker?: TrackLike;
};

type Track = (name: string, data?: EventData) => void;

function sender(tracker: TrackLike): Track {
  return (name, data) => {
    // a failed send (blocked gateway, opt-out) must never break the dapp's
    // wallet flow, and track() already reports bad input by rejecting
    void tracker.track(name, data).catch(() => undefined);
  };
}

const attached = new WeakSet<object>();

// subscribes to the kit and returns a function that stops capture.
//
// call it after StellarWalletsKit.init(). the kit fires STATE_UPDATE (and
// WALLET_SELECTED for a remembered wallet) synchronously while a listener is
// added; those launch-time firings only describe the restored session, so
// they set the starting state and send nothing. a returning visitor whose
// wallet is still connected is therefore not counted as a new connection.
export function attachWalletsKit(kit: WalletsKitLike, options: AttachOptions = {}): () => void {
  const tracker =
    options.tracker ??
    (typeof window === "undefined" ? undefined : (window as { umami?: TrackLike }).umami);
  if (!tracker) {
    throw new Error("umami: attachWalletsKit needs the tracker; call init() first or pass it");
  }
  // a second subscription would count every wallet event twice
  if (attached.has(kit)) throw new Error("umami: this wallets kit is already attached");
  const send = sender(tracker);

  let launching = true;
  let hasAddress = false;
  let walletId: string | undefined;

  const unsubscribe = [
    kit.on(STATE_UPDATE, (event) => {
      const now = Boolean(event.payload.address);
      const connected = now && !hasAddress;
      hasAddress = now;
      if (connected && !launching) {
        send(WALLET_CONNECTED, { network: networkName(event.payload.networkPassphrase) });
      }
    }),
    kit.on(WALLET_SELECTED_EVENT, (event) => {
      const id = event.payload.id || undefined;
      const changed = id !== undefined && id !== walletId;
      walletId = id;
      if (changed && !launching) send(WALLET_SELECTED, { wallet: id });
    }),
    kit.on(DISCONNECT, () => {
      if (!launching) send(WALLET_DISCONNECTED);
    }),
  ];
  launching = false;
  attached.add(kit);

  let detached = false;
  return () => {
    if (detached) return;
    detached = true;
    attached.delete(kit);
    for (const stop of unsubscribe) stop();
  };
}

type KitStorageState = { selected: string | undefined; hasAddress: boolean };

// reads which kit keys exist by their names only. the address key's value is
// never read; the selected wallet id is the only value taken.
function readKitStorage(storage: Storage): KitStorageState {
  let hasAddress = false;
  let hasSelected = false;
  for (let i = 0; i < storage.length; i++) {
    const key = storage.key(i);
    if (key === KIT_ADDRESS_KEY) hasAddress = true;
    else if (key === KIT_SELECTED_KEY) hasSelected = true;
  }
  const selected = hasSelected ? storage.getItem(KIT_SELECTED_KEY) || undefined : undefined;
  return { selected, hasAddress };
}

// the script-tag mode. the kit keeps the network in memory only, so
// wallet_connected carries no network here; attachWalletsKit has it.
// returns a function that stops watching.
export function watchWalletsKitStorage(
  win: Window,
  track: (name: string, data?: EventData) => Promise<void>,
): () => void {
  let storage: Storage;
  try {
    storage = win.localStorage;
    // a storage-blocked document throws here rather than on the property
    void storage.length;
  } catch {
    // no storage means the kit cannot persist anything either, so there is
    // nothing to watch
    return () => undefined;
  }
  const send = sender({ track });

  // the state at start is the restored session, which is not a new event
  let last = readKitStorage(storage);

  const check = () => {
    let now: KitStorageState;
    try {
      now = readKitStorage(storage);
    } catch {
      return;
    }
    if (now.selected !== undefined && now.selected !== last.selected) {
      send(WALLET_SELECTED, { wallet: now.selected });
    }
    if (now.hasAddress && !last.hasAddress) send(WALLET_CONNECTED);
    const hadSession = last.hasAddress || last.selected !== undefined;
    const hasSession = now.hasAddress || now.selected !== undefined;
    if (hadSession && !hasSession) send(WALLET_DISCONNECTED);
    last = now;
  };

  // a change made in another tab of the same site raises a storage event
  // here. that tab counts its own wallet events, so this one only takes the
  // new state as its starting point.
  const onStorage = (e: StorageEvent) => {
    if (e.key === null || e.key === KIT_ADDRESS_KEY || e.key === KIT_SELECTED_KEY) {
      try {
        last = readKitStorage(storage);
      } catch {
        // keep the previous state; the next poll tries again
      }
    }
  };

  const timer = win.setInterval(check, STORAGE_POLL_MS);
  win.addEventListener("storage", onStorage);
  return () => {
    win.clearInterval(timer);
    win.removeEventListener("storage", onStorage);
  };
}
