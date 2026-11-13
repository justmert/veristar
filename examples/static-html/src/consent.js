// docs:start consent
// the site's own consent banner. the tracker tag carries
// data-auto-track="false", so sdk.js loads but sends nothing until start().
// start() sends the pageview of the current page and from then on counts
// route changes and data-umami-event clicks, exactly as without the attribute.
// the choice is kept by the site, not by the tracker, which stores nothing.
const KEY = "example-analytics-consent";

function startAnalytics() {
  // the tracker may be blocked by the visitor, so the call is optional
  window.umami?.start();
}

function choose(granted) {
  localStorage.setItem(KEY, granted ? "granted" : "denied");
  document.getElementById("consent-banner").hidden = true;
  if (granted) startAnalytics();
}

document.getElementById("consent-accept").addEventListener("click", () => choose(true));
document.getElementById("consent-decline").addEventListener("click", () => choose(false));

// a visitor who agreed on an earlier visit is counted from the first page
if (localStorage.getItem(KEY) === "granted") {
  document.getElementById("consent-banner").hidden = true;
  startAnalytics();
}
// docs:end consent
