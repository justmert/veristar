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
