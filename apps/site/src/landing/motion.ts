// the scripted motion of the landing page, ported from the approved design:
// the hero sweep loop, the problem line that stops and strikes each step,
// the performance figures that roll once, and eased wheel scrolling. css
// carries every other motion. a visitor who asks for reduced motion gets
// the final state of each part and no movement.

// tuning of the scripted motion. css durations live in tokens.css; these
// drive frame loops and timers that css cannot express.
const HERO = { first: 1400, hold: 4200, rest: 2400, retry: 800 };
const LINE = { speed: 190, brake: 130, hold: 1300, start: 450, lead: 160 };
const WHEEL = { ease: 0.11, settle: 0.5 };

type Perf = { arm: () => void; fire: () => void };

function reduced(): boolean {
  return (
    typeof window.matchMedia === "function" &&
    window.matchMedia("(prefers-reduced-motion: reduce)").matches
  );
}

// the hero: each project's own numbers for a moment, then the veristar sweep
// puts every project on one baseline, a hold, and back. only while on screen.
function heroLoop(root: HTMLElement): () => void {
  const hm = root.querySelector<HTMLElement>(".hm");
  if (!hm) return () => {};
  if (reduced()) {
    hm.classList.add("on");
    return () => {};
  }
  let seen = true;
  let timer = 0;
  const cycle = () => {
    if (!seen) {
      timer = window.setTimeout(cycle, HERO.retry);
      return;
    }
    hm.classList.add("on");
    timer = window.setTimeout(() => {
      hm.classList.remove("on");
      timer = window.setTimeout(cycle, HERO.rest);
    }, HERO.hold);
  };
  timer = window.setTimeout(cycle, HERO.first);
  const io =
    "IntersectionObserver" in window
      ? new IntersectionObserver((es) => {
          seen = es[0]?.isIntersecting ?? true;
        })
      : null;
  io?.observe(hm);
  return () => {
    window.clearTimeout(timer);
    io?.disconnect();
  };
}

// the problem line moves on its own. each step eases to a stop at the edge of
// the page text, is struck through and gets veristar's fix, then the line moves on.
function problemLine(root: HTMLElement): () => void {
  const mq = root.querySelector<HTMLElement>(".mq");
  const run = mq?.querySelector<HTMLElement>(".mq-run");
  if (!mq || !run) return () => {};
  const place = () => {
    const w = root.querySelector<HTMLElement>(".problem .wrap");
    if (!w) return;
    const pad = Number.parseFloat(getComputedStyle(w).paddingLeft) || 0;
    const left = w.getBoundingClientRect().left - mq.getBoundingClientRect().left + pad;
    mq.style.setProperty("--mq-l", `${Math.round(left)}px`);
  };
  place();
  window.addEventListener("resize", place);
  const steps = Array.from(run.querySelectorAll<HTMLElement>(".mq-p"));
  const per = steps.length / 3;
  const first = steps[0];
  if (!first || per < 1) return () => window.removeEventListener("resize", place);
  if (reduced()) {
    for (const s of steps.slice(0, per)) s.classList.add("struck");
    return () => window.removeEventListener("resize", place);
  }
  const at = (k: number) => -((steps[k]?.offsetLeft ?? 0) - first.offsetLeft);
  const lap = () => (steps[per]?.offsetLeft ?? 0) - first.offsetLeft;
  let x = at(0) + LINE.lead;
  let mode: "start" | "move" | "hold" = "start";
  let since = 0;
  let last: number | null = null;
  let seen = false;
  let idx = 0;
  let raf = 0;
  run.style.transform = `translate3d(${x}px,0,0)`;
  const frame = (now: number) => {
    const dt = last === null ? 0 : Math.min(64, now - last) / 1000;
    last = now;
    if (seen) {
      const stopAt = at(idx);
      if (mode === "hold") {
        since += dt * 1000;
        if (since >= LINE.hold) {
          mode = "start";
          since = 0;
          idx++;
        }
      } else {
        let v = LINE.speed;
        if (mode === "start") {
          since += dt * 1000;
          const k = Math.min(1, since / LINE.start);
          v = LINE.speed * k * k * (3 - 2 * k);
          if (k >= 1) mode = "move";
        }
        const d = x - stopAt;
        if (d <= LINE.brake)
          v = Math.min(v, Math.max(8, LINE.speed * Math.sqrt(Math.max(0, d) / LINE.brake)));
        x -= v * dt;
        if (x <= stopAt) {
          x = stopAt;
          mode = "hold";
          since = 0;
          steps[idx]?.classList.add("struck");
        }
      }
      // after a full lap, move the line back one copy and carry the strikes with it
      if (idx >= per * 2) {
        run.classList.add("jump");
        steps.forEach((s, j) => {
          s.classList.toggle("struck", steps[j + per]?.classList.contains("struck") ?? false);
        });
        x += lap();
        idx -= per;
        void run.offsetWidth;
        run.classList.remove("jump");
      }
      run.style.transform = `translate3d(${x.toFixed(2)}px,0,0)`;
    }
    raf = window.requestAnimationFrame(frame);
  };
  const io =
    "IntersectionObserver" in window
      ? new IntersectionObserver((es) => {
          seen = es[0]?.isIntersecting ?? false;
          if (!seen) last = null;
        })
      : null;
  if (io) io.observe(mq);
  else seen = true;
  raf = window.requestAnimationFrame(frame);
  return () => {
    window.cancelAnimationFrame(raf);
    io?.disconnect();
    window.removeEventListener("resize", place);
  };
}

// the performance figures start at zero and roll once to their measured value
// when they come into view. without an observer they show the measured value.
function rollFigures(root: HTMLElement, perf: Perf): () => void {
  const odos = root.querySelector(".odos");
  if (!odos || !("IntersectionObserver" in window) || reduced()) return () => {};
  perf.arm();
  let raf = 0;
  const io = new IntersectionObserver(
    (es) => {
      if (!es[0]?.isIntersecting) return;
      io.disconnect();
      raf = window.requestAnimationFrame(() => {
        raf = window.requestAnimationFrame(perf.fire);
      });
    },
    { threshold: 0.3 },
  );
  io.observe(odos);
  return () => {
    io.disconnect();
    window.cancelAnimationFrame(raf);
  };
}

// eased wheel scrolling. only for a wheel on this document, never for touch,
// pinch zoom or sideways scrolls, and never with reduced motion.
function easedWheel(): () => void {
  if (reduced()) return () => {};
  const doc = document.scrollingElement ?? document.documentElement;
  let target = window.scrollY;
  let current = window.scrollY;
  let running = false;
  const max = () => doc.scrollHeight - window.innerHeight;
  const step = () => {
    current += (target - current) * WHEEL.ease;
    if (Math.abs(target - current) < WHEEL.settle) {
      current = target;
      running = false;
    }
    window.scrollTo({ top: current, behavior: "instant" });
    if (running) window.requestAnimationFrame(step);
  };
  const wheel = (e: WheelEvent) => {
    if (e.ctrlKey || Math.abs(e.deltaX) > Math.abs(e.deltaY) || max() <= 0) return;
    e.preventDefault();
    if (!running) {
      target = window.scrollY;
      current = window.scrollY;
    }
    const unit = e.deltaMode === 1 ? 16 : e.deltaMode === 2 ? window.innerHeight : 1;
    target = Math.max(0, Math.min(max(), target + e.deltaY * unit));
    if (!running) {
      running = true;
      window.requestAnimationFrame(step);
    }
  };
  const scroll = () => {
    if (!running) {
      target = window.scrollY;
      current = window.scrollY;
    }
  };
  window.addEventListener("wheel", wheel, { passive: false });
  window.addEventListener("scroll", scroll, { passive: true });
  return () => {
    window.removeEventListener("wheel", wheel);
    window.removeEventListener("scroll", scroll);
  };
}

export function startMotion(root: HTMLElement, perf: Perf): () => void {
  const stops = [problemLine(root), heroLoop(root), rollFigures(root, perf), easedWheel()];
  return () => {
    for (const stop of stops) stop();
  };
}
