// the wallets kit modules the console offers (see index.ts for why these and
// not the others). they load on first use, so the sign-in page does not carry
// the wallet libraries until someone picks a wallet.

// the part of the kit's ModuleInterface the console calls
export interface WalletModule {
  productId: string;
  productName: string;
  productUrl: string;
  isAvailable(): Promise<boolean>;
  getAddress(): Promise<{ address: string }>;
  signTransaction(
    xdr: string,
    opts?: { networkPassphrase?: string; address?: string },
  ): Promise<{ signedTxXdr: string; signerAddress?: string }>;
  signMessage(
    message: string,
    opts?: { networkPassphrase?: string; address?: string },
  ): Promise<{ signedMessage: string; signerAddress?: string }>;
}

// the xbull extension injects window.xBullSDK. the kit's xbull module talks
// to it through @creit.tech/xbull-wallet-connect, which is agpl-3.0 and would
// put that licence on the console bundle; it also falls back to a
// wallet.xbull.app popup, which cross-origin-opener-policy same-origin cuts
// off. this module calls the injected api directly, as that library does in
// its extension mode: each call answers a result or { error }.
type XBullError = { error: { message: string; code: number } };
interface XBullSDK {
  getAddress(): Promise<{ address: string } | XBullError>;
  signTransaction(p: {
    xdr: string;
    opts: { networkPassphrase?: string; address?: string };
  }): Promise<{ signedTxXdr: string } | XBullError>;
  signMessage(
    message: string,
    opts: { networkPassphrase?: string; address?: string },
  ): Promise<{ signedMessage: string; signerAddress: string } | XBullError>;
}

function xBullSDK(): XBullSDK | undefined {
  return typeof window === "undefined"
    ? undefined
    : (window as unknown as { xBullSDK?: XBullSDK }).xBullSDK;
}

function xBullResult<T extends object>(r: T | XBullError): T {
  if ("error" in r) throw r.error;
  return r;
}

export class XBullExtensionModule implements WalletModule {
  productId = "xbull";
  productName = "xBull Wallet";
  productUrl = "https://xbull.app";

  async isAvailable(): Promise<boolean> {
    return xBullSDK() !== undefined;
  }

  private sdk(): XBullSDK {
    const s = xBullSDK();
    if (!s) throw new Error("xBull Wallet is not installed");
    return s;
  }

  async getAddress(): Promise<{ address: string }> {
    return xBullResult(await this.sdk().getAddress());
  }

  async signTransaction(
    xdr: string,
    opts?: { networkPassphrase?: string; address?: string },
  ): Promise<{ signedTxXdr: string; signerAddress?: string }> {
    const r = xBullResult(await this.sdk().signTransaction({ xdr, opts: opts ?? {} }));
    return { signedTxXdr: r.signedTxXdr, signerAddress: opts?.address };
  }

  async signMessage(
    message: string,
    opts?: { networkPassphrase?: string; address?: string },
  ): Promise<{ signedMessage: string; signerAddress?: string }> {
    return xBullResult(await this.sdk().signMessage(message, opts ?? {}));
  }
}

export async function loadWalletModules(): Promise<WalletModule[]> {
  const [varial, lobstr, hana, rabet, klever, onekey, bitget, cactuslink, dcent, fordefi, scopuly] =
    await Promise.all([
      import("@creit.tech/stellar-wallets-kit/modules/varial"),
      import("@creit.tech/stellar-wallets-kit/modules/lobstr"),
      import("@creit.tech/stellar-wallets-kit/modules/hana"),
      import("@creit.tech/stellar-wallets-kit/modules/rabet"),
      import("@creit.tech/stellar-wallets-kit/modules/klever"),
      import("@creit.tech/stellar-wallets-kit/modules/onekey"),
      import("@creit.tech/stellar-wallets-kit/modules/bitget"),
      import("@creit.tech/stellar-wallets-kit/modules/cactuslink"),
      import("@creit.tech/stellar-wallets-kit/modules/dcent"),
      import("@creit.tech/stellar-wallets-kit/modules/fordefi"),
      import("@creit.tech/stellar-wallets-kit/modules/scopuly"),
    ]);
  const list: WalletModule[] = [
    new varial.VarialModule(),
    new XBullExtensionModule(),
    new lobstr.LobstrModule(),
    new hana.HanaModule(),
    new rabet.RabetModule(),
    new klever.KleverModule(),
    new onekey.OneKeyModule(),
    new bitget.BitgetModule(),
    new cactuslink.CactusLinkModule(),
    new dcent.DcentModule(),
    new fordefi.FordefiModule(),
    new scopuly.ScopulyModule(),
  ] as WalletModule[];
  // the deterministic test signer exists only in a build made with
  // VITE_STELLAR_TEST_SIGNER=1 (the console end-to-end suite). vite replaces
  // the variable at build time, so a production build drops this branch and
  // the test-signer chunk is never emitted.
  if (import.meta.env.VITE_STELLAR_TEST_SIGNER === "1") {
    const { TestSignerModule } = await import("./test-signer");
    list.unshift(new TestSignerModule());
  }
  return list;
}
