<script lang="ts">
let { children } = $props();
</script>

<nav>
  <a href="/" id="nav-home">Home</a>
  <a href="/pricing" id="nav-pricing">Pricing</a>
  <a href="/docs/getting-started" id="nav-docs">Docs</a>
</nav>
<main>
  {@render children()}
</main>
