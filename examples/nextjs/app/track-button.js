"use client";

// docs:start track
// a click handler needs a client component. the tracker may be blocked by the
// visitor, so the call is optional.
export function TrackButton() {
  return (
    <button
      type="button"
      id="manual"
      onClick={() => void window.umami?.track("manual-call", { source: "nextjs" })}
    >
      Track by call
    </button>
  );
}
// docs:end track
