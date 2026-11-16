// test build only. a wallet module with a fixed keypair that signs without a
// person, so the end-to-end suite can drive wallet sign-in in a browser that
// has no wallet extension. modules.ts imports this file only when the build
// sets VITE_STELLAR_TEST_SIGNER=1; tests/e2e-console checks that a production
// build contains no trace of it (the product id below).

import { hash, Keypair, TransactionBuilder } from "@stellar/stellar-sdk/base";
import type { WalletModule } from "./modules";

export const TEST_SIGNER_ID = "veristar-test-signer";

// sha-256 of "veristar console e2e test signer": a public, fixed seed. the
// account holds nothing on any network and the seed is in the repository,
// so it proves nothing outside a test stack.
const SEED = hash(new TextEncoder().encode("veristar console e2e test signer"));

export function testSignerKeypair(): Keypair {
  return Keypair.fromRawEd25519Seed(SEED);
}

export class TestSignerModule implements WalletModule {
  productId = TEST_SIGNER_ID;
  productName = "Test signer";
  productUrl = "https://example.invalid/";
  private kp = testSignerKeypair();

  async isAvailable(): Promise<boolean> {
    return true;
  }

  async getAddress(): Promise<{ address: string }> {
    return { address: this.kp.publicKey() };
  }

  async signTransaction(
    xdr: string,
    opts?: { networkPassphrase?: string; address?: string },
  ): Promise<{ signedTxXdr: string; signerAddress?: string }> {
    if (!opts?.networkPassphrase) throw new Error("test signer needs a network passphrase");
    const tx = TransactionBuilder.fromXDR(xdr, opts.networkPassphrase);
    tx.sign(this.kp);
    return { signedTxXdr: tx.toXDR(), signerAddress: this.kp.publicKey() };
  }

  async signMessage(message: string): Promise<{ signedMessage: string; signerAddress?: string }> {
    const sig = this.kp.signMessage(message);
    return { signedMessage: btoa(String.fromCharCode(...sig)), signerAddress: this.kp.publicKey() };
  }
}
