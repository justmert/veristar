// @vitest-environment node
// the stellar sdk checks Uint8Array with instanceof, which fails across the
// jsdom and node realms; the helper itself touches no dom
import {
  Account,
  Keypair,
  Networks,
  Operation,
  Transaction,
  TransactionBuilder,
} from "@stellar/stellar-sdk/base";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { WalletModule } from "@/lib/stellar-wallet/modules";

const fake: { list: WalletModule[] } = { list: [] };

vi.mock("@/lib/stellar-wallet/modules", () => ({
  loadWalletModules: async () => fake.list,
}));

const {
  connectWallet,
  listWallets,
  resetWalletModulesForTest,
  shortAddress,
  signMessage,
  signTransaction,
  STELLAR_PUBLIC_PASSPHRASE,
  WalletError,
} = await import("@/lib/stellar-wallet");
const { TestSignerModule, testSignerKeypair, TEST_SIGNER_ID } = await import(
  "@/lib/stellar-wallet/test-signer"
);

const OTHER = Keypair.random().publicKey();

function module(over: Partial<WalletModule> & { productId: string }): WalletModule {
  return {
    productName: over.productId,
    productUrl: "https://wallet.example/",
    isAvailable: async () => true,
    getAddress: async () => ({ address: OTHER }),
    signTransaction: async () => ({ signedTxXdr: "AAAA", signerAddress: OTHER }),
    signMessage: async () => ({ signedMessage: "c2ln", signerAddress: OTHER }),
    ...over,
  };
}

afterEach(() => {
  resetWalletModulesForTest();
  fake.list = [];
});

describe("stellar wallet helper", () => {
  it("lists installed wallets first and marks message signing", async () => {
    fake.list = [
      module({ productId: "zeta", productName: "Zeta", isAvailable: async () => true }),
      module({ productId: "rabet", productName: "Rabet", isAvailable: async () => false }),
      module({
        productId: "alpha",
        productName: "Alpha",
        isAvailable: async () => {
          throw new Error("detection failed");
        },
      }),
    ];
    const ws = await listWallets();
    expect(ws.map((w) => [w.id, w.available, w.signsMessages])).toEqual([
      ["zeta", true, true],
      ["alpha", false, true],
      ["rabet", false, false],
    ]);
  });

  it("refuses a wallet that is not installed", async () => {
    fake.list = [module({ productId: "a", isAvailable: async () => false })];
    await expect(connectWallet("a")).rejects.toMatchObject({ code: "unavailable" });
  });

  it("maps a declined request to rejected", async () => {
    fake.list = [
      module({
        productId: "a",
        getAddress: async () => {
          throw { code: -1, message: "User declined access" };
        },
      }),
    ];
    const err = await connectWallet("a").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(WalletError);
    expect(err).toMatchObject({ code: "rejected" });
  });

  it("refuses a signature from another account", async () => {
    fake.list = [module({ productId: "a" })];
    const me = Keypair.random().publicKey();
    await expect(signTransaction("a", "AAAA", { address: me })).rejects.toMatchObject({
      code: "wrong_account",
    });
    await expect(signMessage("a", "hello", { address: me })).rejects.toMatchObject({
      code: "wrong_account",
    });
  });

  it("does not ask rabet for a message signature", async () => {
    const sm = vi.fn();
    fake.list = [module({ productId: "rabet", signMessage: sm })];
    await expect(signMessage("rabet", "x", { address: OTHER })).rejects.toMatchObject({
      code: "unsupported",
    });
    expect(sm).not.toHaveBeenCalled();
  });

  it("shortens an address to four and four", () => {
    expect(shortAddress("GCB634SLKV5N4COK4FICWJADNTFWIHTWWM34BFPLACL6YSQVLY5ZWOQN")).toBe(
      "GCB6…WOQN",
    );
  });
});

describe("test signer", () => {
  it("signs a transaction on the requested network with its fixed key", async () => {
    fake.list = [new TestSignerModule()];
    const kp = testSignerKeypair();
    const tx = new TransactionBuilder(new Account(kp.publicKey(), "-1"), {
      fee: "100",
      networkPassphrase: Networks.PUBLIC,
    })
      .addOperation(Operation.manageData({ name: "example.com auth", value: "x".repeat(64) }))
      .setTimeout(300)
      .build();
    const address = await connectWallet(TEST_SIGNER_ID);
    expect(address).toBe(kp.publicKey());
    const signed = await signTransaction(TEST_SIGNER_ID, tx.toXDR(), { address });
    const back = new Transaction(signed, STELLAR_PUBLIC_PASSPHRASE);
    expect(back.signatures).toHaveLength(1);
    const [sig] = back.signatures;
    if (!sig) throw new Error("no signature");
    expect(kp.verify(back.hash(), sig.signature)).toBe(true);
  });

  it("signs a sep-53 message", async () => {
    fake.list = [new TestSignerModule()];
    const kp = testSignerKeypair();
    const { signature } = await signMessage(TEST_SIGNER_ID, "Hello, World!", {
      address: kp.publicKey(),
    });
    const raw = Uint8Array.from(atob(signature), (c) => c.charCodeAt(0));
    expect(kp.verifyMessage("Hello, World!", raw)).toBe(true);
  });
});
