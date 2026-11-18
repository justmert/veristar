// docs:start track
// the tracker may be blocked by the visitor, so the call is optional
export default function TrackButton() {
  const onClick = () => {
    void window.umami?.track("manual-call", { source: "docusaurus" });
  };
  return (
    <button type="button" id="manual" onClick={onClick}>
      Track by call
    </button>
  );
}
// docs:end track

declare global {
  interface Window {
    umami?: {
      track: (name: string, data?: Record<string, string | number | boolean>) => Promise<void>;
    };
  }
}
