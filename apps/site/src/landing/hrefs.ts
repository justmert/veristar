// every address the landing page links to. on the site's own hosts the
// domain comes from the address bar (see ../links.ts), so the same build
// works after a handover. on a preview host (localhost, an address) there is
// no domain to read, so the links point at the public veristar.app hosts
// instead of disappearing, and the page keeps its layout.
import { site } from "../links";

const PREVIEW_DOMAIN = "veristar.app";
const domain = site.domain ?? PREVIEW_DOMAIN;

export const hrefs = {
  signIn: site.signIn ?? `https://app.${domain}/sign-in`,
  console: `https://app.${domain}`,
  docsHome: `https://docs.${domain}`,
  docs: (path: string) => `https://docs.${domain}/${path.replace(/^\//, "")}`,
  status: `https://status.${domain}`,
  // null while the repository is private (see ../links.ts)
  repo: site.repo,
};
