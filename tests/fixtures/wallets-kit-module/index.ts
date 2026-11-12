// test fixture only, never product code and never in the documentation. a
// stellar wallets kit module (the kit's ModuleInterface) that answers like a
// browser wallet extension with a fixed testnet account, so the real kit runs
// its own code paths (auth modal, selected module, address and network
// signals, localStorage, disconnect) and fires its real events without an
// extension in the test browser. it signs nothing: the this stage flows never
// sign, and a call that would sign throws so a test that tries is loud.
//
// it has no runtime imports, so it works with every kit version under test
// (2.7.1 and 2.8.0) and needs no resolution from the importing package.

// a randomly generated public key with no funds on any network. every this stage
// test asserts that this exact string reaches no request body, no queued
// message and no umami table.
export const TEST_WALLET_ADDRESS = "GCB634SLKV5N4COK4FICWJADNTFWIHTWWM34BFPLACL6YSQVLY5ZWOQN";
export const TEST_WALLET_ID = "sa-test-wallet";
export const TEST_WALLET_NAME = "SA Test Wallet";
const TESTNET_PASSPHRASE = "Test SDF Network ; September 2015";

// a 1x1 transparent gif, so the kit's modal shows no broken image and makes no request
const ICON = "data:image/gif;base64,R0lGODlhAQABAAAAACH5BAEKAAEALAAAAAABAAEAAAICTAEAOw==";

function refuse(method: string): never {
  throw { code: -3, message: `${TEST_WALLET_NAME} does not implement ${method}` };
}

export class TestWalletModule {
  // the kit's ModuleType.HOT_WALLET value; a string so no kit import is needed
  moduleType = "HOT_WALLET" as const;
  productId = TEST_WALLET_ID;
  productName = TEST_WALLET_NAME;
  productUrl = "https://sa-test-wallet.example/";
  productIcon = ICON;
  // how many times the kit asked for the address, for unit tests
  addressRequests = 0;

  async isAvailable(): Promise<boolean> {
    return true;
  }

  async isPlatformWrapper(): Promise<boolean> {
    return false;
  }

  async getAddress(): Promise<{ address: string }> {
    this.addressRequests++;
    return { address: TEST_WALLET_ADDRESS };
  }

  async getNetwork(): Promise<{ network: string; networkPassphrase: string }> {
    return { network: "TESTNET", networkPassphrase: TESTNET_PASSPHRASE };
  }

  async signTransaction(): Promise<{ signedTxXdr: string }> {
    return refuse("signTransaction");
  }

  async signAuthEntry(): Promise<{ signedAuthEntry: string }> {
    return refuse("signAuthEntry");
  }

  async signMessage(): Promise<{ signedMessage: string }> {
    return refuse("signMessage");
  }

  async disconnect(): Promise<void> {}
}
