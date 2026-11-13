<script lang="ts">
// docs:start track
// the tracker may be blocked by the visitor, so the call is optional
function manualCall() {
  void window.umami?.track("manual-call", { source: "sveltekit" });
}
// docs:end track
</script>

<svelte:head>
  <title>SvelteKit example: home</title>
</svelte:head>

<h1>SvelteKit example</h1>
<p>Server rendered on the first load, client-side navigation after that.</p>
<button type="button" id="signup" data-umami-event="signup-click" data-umami-event-plan="free">
  Sign up
</button>
<button type="button" id="manual" onclick={manualCall}>Track by call</button>
