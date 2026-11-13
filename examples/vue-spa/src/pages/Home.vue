<script setup lang="ts">
// docs:start track
// the tracker may be blocked by the visitor, so the call is optional
function manualCall() {
  void window.umami?.track("manual-call", { source: "vue-spa" });
}
// docs:end track
</script>

<template>
  <h1>Vue example</h1>
  <p>Client-side routing. Every route change is one pageview.</p>
  <!-- docs:start event -->
  <button type="button" id="signup" data-umami-event="signup-click" data-umami-event-plan="free">
    Sign up
  </button>
  <!-- docs:end event -->
  {{ " " }}
  <button type="button" id="manual" @click="manualCall">Track by call</button>
</template>
