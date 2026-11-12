// npm package mode: the tracker is bundled with the dapp and subscribes to
// the kit's own events with one call
import { init } from "tracker";
import { attachWalletsKit } from "tracker/wallets-kit";
import { render } from "./app.tsx";
import { initKit } from "./wallets.ts";

declare const __SA_SITE_ID__: string;
declare const __SA_GATEWAY_ORIGIN__: string;

init({ siteId: __SA_SITE_ID__, host: __SA_GATEWAY_ORIGIN__ });
// after the kit's init, so the restored session is the starting point
attachWalletsKit(await initKit());
render("module");
