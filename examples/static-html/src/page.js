// docs:start track
// umami.track() from page code; the tracker may be blocked, so check first
document.getElementById("manual").addEventListener("click", () => {
  window.umami?.track("manual-call", { source: "static-html" });
});
// docs:end track

// docs:start opt-out
// an opt-out control for the site's privacy notice. the tracker checks this
// flag before every send, in this browser, from the next event on.
document.getElementById("opt-out").addEventListener("click", () => {
  localStorage.setItem("umami.disabled", "1");
  document.getElementById("opt-out").textContent = "Analytics turned off for this browser";
});
// docs:end opt-out
