<template>
  <!-- vitepress's router takes over clicks on links inside the site and
       changes the url with history.pushState -->
  <nav>
    <a href="/" id="nav-home">Home</a>
    {{ " " }}
    <a href="/pricing" id="nav-pricing">Pricing</a>
    {{ " " }}
    <a href="/docs/getting-started" id="nav-docs">Docs</a>
  </nav>
</template>
