import type { Theme } from "vitepress";
import DefaultTheme from "vitepress/theme";
import Nav from "./Nav.vue";
import TrackButton from "./TrackButton.vue";

export default {
  extends: DefaultTheme,
  enhanceApp({ app }) {
    app.component("ExampleNav", Nav);
    app.component("TrackButton", TrackButton);
  },
} satisfies Theme;
