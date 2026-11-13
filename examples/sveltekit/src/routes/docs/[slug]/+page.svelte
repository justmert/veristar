<script lang="ts">
import { page } from "$app/state";
</script>

<svelte:head>
  <title>SvelteKit example: {page.params.slug}</title>
</svelte:head>

<h1>Docs: {page.params.slug}</h1>
