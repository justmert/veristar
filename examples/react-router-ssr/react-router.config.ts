import type { Config } from "@react-router/dev/config";

// framework mode with server rendering: every full page load is rendered on the
// server, later navigations happen on the client
export default {
  ssr: true,
} satisfies Config;
