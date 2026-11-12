// starts the browser proxy and the tenant-side sites for the whole run; the
// returned function is playwright's teardown.
import { startServers } from "./servers.ts";

export default async function globalSetup(): Promise<() => Promise<void>> {
  const f = await startServers();
  return () => f.close();
}
