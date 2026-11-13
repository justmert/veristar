import { type ModuleInterface, Networks, StellarWalletsKit } from "@creit.tech/stellar-wallets-kit";
import { defaultModules } from "@creit.tech/stellar-wallets-kit/modules/utils";

// the wallet modules the kit offers. the wallettest build swaps in the test
// wallet from tests/fixtures, because a test browser has no wallet extension;
// vite replaces import.meta.env.MODE with a constant, so a normal build drops
// that branch and its import entirely.
async function walletModules(): Promise<ModuleInterface[]> {
  if (import.meta.env.MODE === "wallettest") {
    const { TestWalletModule } = await import(
      "../../../tests/fixtures/wallets-kit-module/index.ts"
    );
    return [new TestWalletModule() as unknown as ModuleInterface];
  }
  return defaultModules();
}

// docs:start kit
// the example dapp runs on testnet
export async function initKit(): Promise<typeof StellarWalletsKit> {
  StellarWalletsKit.init({ modules: await walletModules(), network: Networks.TESTNET });
  return StellarWalletsKit;
}
// docs:end kit
