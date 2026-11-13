<script setup lang="ts">
import { useRoute } from "vue-router";

const route = useRoute();
</script>

<template>
  <h1>Docs: {{ route.params.slug }}</h1>
</template>
