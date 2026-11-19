/// <reference types="vite/client" />

interface ImportMetaEnv {
  // the console's origin for a build served on a host that names no domain
  // (localhost, a preview host); unset hides the sign-in links there
  readonly VITE_CONSOLE_ORIGIN?: string;
  // the public source repository; unset while it is private, which hides its links
  readonly VITE_REPO_URL?: string;
}
