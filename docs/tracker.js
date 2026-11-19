// mintlify runs every .js file of the docs on every page, once per visit.
// this one measures the veristar docs with the veristar.app site, whose
// allowed origins include https://docs.veristar.app.
const tracker = document.createElement("script");
tracker.src = "https://in.veristar.app/v/1.4.0/sdk.js";
tracker.integrity = "sha384-8bYExYW1FMGfNg5LETdGYFjtJPLfyY+gJQwWA0rL5xNM7Xl+3wK99GxSV3+siv/A";
tracker.crossOrigin = "anonymous";
tracker.dataset.websiteId = "01a1f1c1-5f8e-7a93-9396-c9847a3d7acd";
document.head.appendChild(tracker);
