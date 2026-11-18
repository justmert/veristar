import "./index.css";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { HomePage } from "./pages/home";

// the home page mounts on its own: its motion is css and plain scripts, so it
// does not load the motion library that ./root.tsx brings for the 404 page
const root = document.getElementById("root");
if (!root) throw new Error("the page has no #root element");
createRoot(root).render(
  <StrictMode>
    <HomePage />
  </StrictMode>,
);
