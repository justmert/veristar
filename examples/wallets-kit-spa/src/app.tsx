import { KitEventType, StellarWalletsKit } from "@creit.tech/stellar-wallets-kit";
import { StrictMode, useEffect, useState } from "react";
import { createRoot } from "react-dom/client";

// window.umami is typed by the tracker package, which declares the global

// the dapp's own wallet ui. it shows only whether a wallet is connected; the
// tracker never sees the address either way.
function App({ mode }: { mode: string }) {
  const [connected, setConnected] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    const stop = StellarWalletsKit.on(KitEventType.STATE_UPDATED, (e) =>
      setConnected(Boolean(e.payload.address)),
    );
    return stop;
  }, []);

  const connect = async () => {
    setError("");
    try {
      await StellarWalletsKit.authModal();
    } catch (e) {
      setError((e as { message?: string }).message ?? String(e));
    }
  };

  return (
    <main>
      <h1>Wallets Kit example</h1>
      <p>
        Capture mode: <span id="mode">{mode}</span>. Wallet:{" "}
        <span id="status">{connected ? "connected" : "not connected"}</span>
      </p>
      {error && <p id="error">{error}</p>}
      <button type="button" id="connect" onClick={() => void connect()} disabled={connected}>
        Connect wallet
      </button>{" "}
      <button
        type="button"
        id="disconnect"
        onClick={() => void StellarWalletsKit.disconnect()}
        disabled={!connected}
      >
        Disconnect
      </button>
      <h2>Custom events</h2>
      <button
        type="button"
        id="signup"
        data-umami-event="signup-click"
        data-umami-event-plan="free"
      >
        Sign up
      </button>{" "}
      <button
        type="button"
        id="manual"
        // the tracker may be blocked by the visitor, so the call is optional
        onClick={() => void window.umami?.track("manual-call", { source: "wallets-kit-spa" })}
      >
        Track by call
      </button>
    </main>
  );
}

export function render(mode: string) {
  const root = document.getElementById("root");
  if (!root) throw new Error("#root is missing from the page");
  createRoot(root).render(
    <StrictMode>
      <App mode={mode} />
    </StrictMode>,
  );
}
