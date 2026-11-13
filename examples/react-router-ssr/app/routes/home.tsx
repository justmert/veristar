declare global {
  interface Window {
    umami?: {
      track: (name: string, data?: Record<string, string | number | boolean>) => Promise<void>;
    };
  }
}

export function meta() {
  return [{ title: "React Router example: home" }];
}

export default function Home() {
  return (
    <>
      <h1>React Router example</h1>
      <p>Server rendered on the first load, client-side navigation after that.</p>
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
        onClick={() => void window.umami?.track("manual-call", { source: "react-router-ssr" })}
      >
        Track by call
      </button>
    </>
  );
}
