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

// the kit's xbull module opens wallet.xbull.app in a popup when the extension
// is missing, which cross-origin-opener-policy same-origin cuts off; only the
// extension path is offered
function extensionOnly(m: WalletModule, present: () => boolean): WalletModule {
  return new Proxy(m, {
    get(target, prop, receiver) {
      if (prop === "isAvailable") return async () => present();
      return Reflect.get(target, prop, receiver);
    },
  });
}

export async function loadWalletModules(): Promise<WalletModule[]> {
  const [
    varial,
    xbull,
    lobstr,
    hana,
    rabet,
    klever,
    onekey,
    bitget,
    cactuslink,
    dcent,
    fordefi,
    scopuly,
  ] = await Promise.all([
    import("@creit.tech/stellar-wallets-kit/modules/varial"),
    import("@creit.tech/stellar-wallets-kit/modules/xbull"),
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
    extensionOnly(
      new xbull.xBullModule(),
      () => typeof window !== "undefined" && "xBullSDK" in window,
    ),
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
