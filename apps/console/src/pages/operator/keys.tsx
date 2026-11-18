import { ApiKeysPage } from "./api-keys";
import { OperatorMcpPage } from "./mcp";

// every read-only key for machines on one page: metrics api keys and the
// keys and connections of ai assistants
export function AccessKeysPage() {
  return (
    <div className="flex flex-col gap-10">
      <ApiKeysPage />
      <OperatorMcpPage />
    </div>
  );
}
