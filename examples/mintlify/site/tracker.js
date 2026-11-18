// mintlify runs every .js file of the docs on every page, once per visit.
// this one adds the tag from the console to the page; each attribute of the
// tag is set on the element, so the browser checks the file against its
// integrity value. the tracker then counts every page change on its own.
// docs:start tracker
const tracker = document.createElement("script");
tracker.src = "https://in.analytics.example/v/1.3.2/sdk.js";
tracker.integrity = "sha384-Q2xIA5ztfj7VBlpI/+6kehWQVUC2jZPr89aAooy7Uinc/V2C0VF7Ti66L3gj6D/v";
tracker.crossOrigin = "anonymous";
tracker.dataset.websiteId = "3f6a0c2e-9b1d-4e7a-8c5f-1d2e3f4a5b6c";
document.head.appendChild(tracker);
// docs:end tracker
