<template>
  <nav>
    <RouterLink to="/" id="nav-home">Home</RouterLink>
    {{ " " }}
    <RouterLink to="/pricing" id="nav-pricing">Pricing</RouterLink>
    {{ " " }}
    <RouterLink to="/docs/getting-started" id="nav-docs">Docs</RouterLink>
  </nav>
  <main>
    <RouterView />
  </main>
</template>
