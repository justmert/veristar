// script tag mode: sdk.js is on the page with data-wallets-kit="true" and
// watches the kit's storage keys itself, so the dapp only sets up the kit
import { render } from "./app.tsx";
import { initKit } from "./wallets.ts";

await initKit();
render("script");
