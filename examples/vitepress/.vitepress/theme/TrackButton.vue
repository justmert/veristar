<script setup lang="ts">
// docs:start track
// the tracker may be blocked by the visitor, so the call is optional
function manualCall() {
  void window.umami?.track("manual-call", { source: "vitepress" });
}
// docs:end track
</script>

<template>
  <button type="button" id="manual" @click="manualCall">Track by call</button>
</template>
