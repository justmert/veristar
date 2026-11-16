// the console's one way to talk to a stellar wallet. every page that needs a
// wallet (wallet sign-in, linking a wallet on the account page, proving
// account ownership elsewhere in the console) imports this module and the
// WalletPicker component, never the wallets kit directly.
//
// it drives the stellar wallets kit through its module api (one module per
// wallet) and never through the kit's own modal or StellarWalletsKit class:
// those import the kit's twind styles, which add a <style> element at load
// time, and the console's csp (style-src 'self') refuses it. the picker is a
// console component instead (components/wallet-picker.tsx).
//
// the wallet list is the kit modules that work under the console's security
// headers: wallets that a browser extension injects into the page. xbull is
// a console module on the extension's own api (modules.ts), because the
// kit's xbull module bundles an agpl-3.0 library. wallets
// that need a cross-origin popup (albedo, xbull's web wallet, ghostsig), a
// websocket relay (walletconnect), an iframe (trezor) or webusb (ledger) are
// left out, because cross-origin-opener-policy same-origin, connect-src
// 'self', frame-src 'none' and permissions-policy usb=() refuse them.
//
// two signing operations are offered:
//   - signTransaction signs a transaction envelope (xdr). wallet sign-in uses
//     it for the sep-10 challenge, which every listed wallet can sign.
//   - signMessage signs an arbitrary message the sep-53 way (the wallet
//     prefixes "Stellar Signed Message:\n", hashes with sha-256 and signs
//     with ed25519). not every wallet implements it; WalletOption.signsMessages
//     says which do, and the picker hides the others when asked to.
//
// usage:
//   const wallets = await listWallets();
//   const address = await connectWallet(wallets[0].id);
//   const signed = await signTransaction(wallets[0].id, xdr, { address });
//   const { signature } = await signMessage(wallets[0].id, "text", { address });

import { loadWalletModules, type WalletModule } from "./modules";

// the network every console signature is bound to. the control plane checks
// challenges against this passphrase only.
export const STELLAR_PUBLIC_PASSPHRASE = "Public Global Stellar Network ; September 2015";

export interface WalletOption {
  // the kit's module id, for example "lobstr"
  id: string;
  name: string;
  // where the wallet can be installed
  url: string;
  // true when the extension is present in this browser
  available: boolean;
  // true when the wallet implements sep-53 message signing
  signsMessages: boolean;
}

export type WalletErrorCode =
  // the extension is not installed or not unlocked
  | "unavailable"
  // the person declined in the wallet
  | "rejected"
  // the wallet signed with another account than the one asked for
  | "wrong_account"
  // the wallet does not implement the operation
  | "unsupported"
  | "failed";

export class WalletError extends Error {
  readonly code: WalletErrorCode;
  constructor(code: WalletErrorCode, message: string) {
    super(message);
    this.name = "WalletError";
    this.code = code;
  }
}

// wallets that cannot sign sep-53 messages, read from the kit 2.8.0 modules:
// each of these rejects signMessage with "does not support". kept here
// because the module interface has no capability flag.
const NO_MESSAGE_SIGNING: ReadonlySet<string> = new Set(["rabet"]);

let modules: Promise<WalletModule[]> | null = null;

function allModules(): Promise<WalletModule[]> {
  modules ??= loadWalletModules();
  return modules;
}

async function moduleFor(id: string): Promise<WalletModule> {
  const m = (await allModules()).find((x) => x.productId === id);
  if (!m) throw new WalletError("unavailable", `no wallet with id ${id}`);
  return m;
}

// listWallets returns every supported wallet, the ones present in this
// browser first, each group in the kit's name order.
export async function listWallets(): Promise<WalletOption[]> {
  const mods = await allModules();
  const out = await Promise.all(
    mods.map(async (m): Promise<WalletOption> => {
      let available = false;
      try {
        available = await m.isAvailable();
      } catch {
        // a module whose detection throws has no usable extension here
        available = false;
      }
      return {
        id: m.productId,
        name: m.productName,
        url: m.productUrl,
        available,
        signsMessages: !NO_MESSAGE_SIGNING.has(m.productId),
      };
    }),
  );
  return out.sort(
    (a, b) => Number(b.available) - Number(a.available) || a.name.localeCompare(b.name),
  );
}

// the kit's modules reject with { code, message }; -4 is never used, and the
// wording of a refusal differs per wallet, so a refusal is recognised by the
// words the extensions use
function toWalletError(e: unknown): WalletError {
  if (e instanceof WalletError) return e;
  const msg =
    (typeof e === "object" && e !== null && "message" in e && typeof e.message === "string"
      ? e.message
      : typeof e === "string"
        ? e
        : "") || "the wallet did not answer";
  if (/does not support|not supported|not implement/i.test(msg)) {
    return new WalletError("unsupported", msg);
  }
  if (/reject|declin|denied|cancel/i.test(msg)) return new WalletError("rejected", msg);
  if (/not installed|not connected|not available|is not open/i.test(msg)) {
    return new WalletError("unavailable", msg);
  }
  return new WalletError("failed", msg);
}

// connectWallet asks the wallet for its active account (the extension shows
// its own permission prompt the first time) and returns the G-address.
export async function connectWallet(id: string): Promise<string> {
  const m = await moduleFor(id);
  if (!(await m.isAvailable().catch(() => false))) {
    throw new WalletError("unavailable", `${m.productName} is not installed in this browser`);
  }
  let address: string;
  try {
    ({ address } = await m.getAddress());
  } catch (e) {
    throw toWalletError(e);
  }
  if (!/^G[A-Z2-7]{55}$/.test(address)) {
    throw new WalletError("failed", `${m.productName} returned no stellar account`);
  }
  return address;
}

export interface SignOptions {
  // the account that must sign; a wallet that answers for another account is
  // refused with wrong_account
  address: string;
  // defaults to the public network
  networkPassphrase?: string;
}

// signTransaction asks the wallet to sign a transaction envelope and returns
// the signed envelope as base64 xdr.
export async function signTransaction(id: string, xdr: string, opts: SignOptions): Promise<string> {
  const m = await moduleFor(id);
  let res: { signedTxXdr: string; signerAddress?: string };
  try {
    res = await m.signTransaction(xdr, {
      address: opts.address,
      networkPassphrase: opts.networkPassphrase ?? STELLAR_PUBLIC_PASSPHRASE,
    });
  } catch (e) {
    throw toWalletError(e);
  }
  if (res.signerAddress && res.signerAddress !== opts.address) {
    throw new WalletError("wrong_account", `${m.productName} signed with another account`);
  }
  if (!res.signedTxXdr) throw new WalletError("failed", `${m.productName} returned no signature`);
  return res.signedTxXdr;
}

export interface MessageSignature {
  // the ed25519 signature over the sep-53 message hash, base64
  signature: string;
  // the account the wallet reports as the signer; the requested address when
  // the wallet does not report one
  signerAddress: string;
}

// signMessage signs message the sep-53 way. the verifier needs the message,
// the address and the signature; see sep-53 "verification".
export async function signMessage(
  id: string,
  message: string,
  opts: SignOptions,
): Promise<MessageSignature> {
  const m = await moduleFor(id);
  if (NO_MESSAGE_SIGNING.has(id)) {
    throw new WalletError("unsupported", `${m.productName} cannot sign messages`);
  }
  let res: { signedMessage: string; signerAddress?: string };
  try {
    res = await m.signMessage(message, {
      address: opts.address,
      networkPassphrase: opts.networkPassphrase ?? STELLAR_PUBLIC_PASSPHRASE,
    });
  } catch (e) {
    throw toWalletError(e);
  }
  const signer = res.signerAddress || opts.address;
  if (signer !== opts.address) {
    throw new WalletError("wrong_account", `${m.productName} signed with another account`);
  }
  if (!res.signedMessage) throw new WalletError("failed", `${m.productName} returned no signature`);
  return { signature: res.signedMessage, signerAddress: signer };
}

// shortAddress is how the console shows an address: the first and last four
// characters, as wallets do. the control plane uses the same form for the
// display name of a wallet-only account.
export function shortAddress(address: string): string {
  return address.length > 12 ? `${address.slice(0, 4)}…${address.slice(-4)}` : address;
}

// for unit tests: forget the loaded modules
export function resetWalletModulesForTest(): void {
  modules = null;
}
