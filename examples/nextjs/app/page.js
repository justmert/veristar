import { TrackButton } from "./track-button.js";

export const metadata = { title: "Next.js example: home" };

export default function Home() {
  return (
    <>
      <h1>Next.js example</h1>
      <p>Server rendered on the first load, client-side navigation after that.</p>
      <button
        type="button"
        id="signup"
        data-umami-event="signup-click"
        data-umami-event-plan="free"
      >
        Sign up
      </button>{" "}
      <TrackButton />
    </>
  );
}
