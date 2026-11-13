---
title: "Vue"
description: "Add the tracker to a Vue app built with Vite and Vue Router."
---

For a Vue app built with Vite, with Vue Router. Tested by the example
[`examples/vue-spa`](https://github.com/justmert/veristar/tree/main/examples/vue-spa) (Vue 3.5.45, Vue Router 5.4.0,
`@vitejs/plugin-vue` 6.0.10, Vite 8), which runs in the browser test matrix.

## Where the tag goes

Put the tag in `index.html` at the root of the project, not in a component:

{/* include examples/vue-spa/index.html region=head */}
```html
<!-- paste the tag from the console in place of the next line -->
<!-- tracker -->
```

The example replaces the comment with the tag at build time so that tests can use their own site
id. In your app the tag from the console stands there:

{/* snippet version=1.1.0 */}
```html
<script defer src="https://in.analytics.example/v/1.1.0/sdk.js" integrity="sha384-lxlitLtIMAeT/K3ieev+MeMGxZe7oceuWLOy8GZAjK7TMKS2Uh9UPmfGLBczUPsq" crossorigin="anonymous" data-website-id="3f6a0c2e-9b1d-4e7a-8c5f-1d2e3f4a5b6c"></script>
```

## Route changes

Vue Router's `createWebHistory()` changes the address with `history.pushState`, which the tracker
watches, so every route change is one pageview without a router hook:

{/* include examples/vue-spa/src/main.ts */}
```ts
import { createApp } from "vue";
import { createRouter, createWebHistory } from "vue-router";
import App from "./App.vue";
import Doc from "./pages/Doc.vue";
import Home from "./pages/Home.vue";
import Pricing from "./pages/Pricing.vue";

// vue router changes the url with history.pushState, which the tracker
// observes, so each route change is one pageview without any router hook
const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: "/", component: Home },
    { path: "/pricing", component: Pricing },
    { path: "/docs/:slug", component: Doc },
  ],
});

createApp(App).use(router).mount("#app");
```

With `createWebHashHistory()` the routes live in the fragment (`/#/pricing`); those are counted too,
because fragments that start with `#/` are sent.

## Custom events

A `data-umami-event` attribute in a template sends an event when the element is clicked:

{/* include examples/vue-spa/src/pages/Home.vue region=event */}
```vue
<button type="button" id="signup" data-umami-event="signup-click" data-umami-event-plan="free">
  Sign up
</button>
```

From a component's code, call `window.umami?.track()`; the visitor may block the tracker, so the
call is optional:

{/* include examples/vue-spa/src/pages/Home.vue region=track */}
```vue
// the tracker may be blocked by the visitor, so the call is optional
function manualCall() {
  void window.umami?.track("manual-call", { source: "vue-spa" });
}
```

For TypeScript, declare the global once:

{/* include examples/vue-spa/src/env.d.ts */}
```ts
// window.umami as sdk.js installs it; optional because a visitor may block it
interface Window {
  umami?: {
    track: (name: string, data?: Record<string, string | number | boolean>) => Promise<void>;
  };
}
```

## What the browser tests check

The same matrix as for every client-routed example: one pageview per route change, back and
forward, the previous route as referrer, an external referrer on the first pageview, both kinds of
custom event, Global Privacy Control and a tampered tracker refused (`tests/e2e/specs`).
