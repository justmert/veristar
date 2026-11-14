import "./index.css";
import { transition } from "@veristar/brand/motion";
import { MotionConfig } from "motion/react";
import { type ReactNode, StrictMode } from "react";
import { createRoot } from "react-dom/client";

// people who ask for reduced motion keep opacity changes only
export function mount(page: ReactNode) {
  const root = document.getElementById("root");
  if (!root) throw new Error("the page has no #root element");
  createRoot(root).render(
    <StrictMode>
      <MotionConfig reducedMotion="user" transition={transition.change}>
        {page}
      </MotionConfig>
    </StrictMode>,
  );
}
