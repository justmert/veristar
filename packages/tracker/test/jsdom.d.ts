// jsdom ships no type declarations and @types/jsdom is not a pinned dependency,
// so only the surface the tests use is declared here
declare module "jsdom" {
  export type JSDOMOptions = {
    url?: string;
    referrer?: string;
    runScripts?: "dangerously" | "outside-only";
    resources?: "usable";
    pretendToBeVisual?: boolean;
    beforeParse?: (window: Window & typeof globalThis) => void;
    virtualConsole?: VirtualConsole;
  };
  export class VirtualConsole {
    on(event: "jsdomError", listener: (err: Error) => void): this;
  }
  export class JSDOM {
    constructor(html?: string, options?: JSDOMOptions);
    readonly window: Window & typeof globalThis;
  }
}
