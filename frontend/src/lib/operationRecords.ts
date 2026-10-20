import type { HostMetrics, IncidentSnapshot } from "../../bindings/github.com/blacknode/blacknode/internal/service/models";
import type { OperationDocument, OperationEvent } from "../../bindings/github.com/blacknode/blacknode/internal/store/models";

// These types travel inside OperationDocument.body as JSON, so Wails does not
// generate them. Keep them aligned with platform_incidents.go.
export type Incident = {
  id: string;
  name: string;
  workspaceID: string;
  status: string;
  createdAt: number;
  hostIDs: string[];
  snapshots: IncidentSnapshot[];
  metrics: Record<string, HostMetrics[] | null | undefined>;
  changes: OperationEvent[];
  logs: Record<string, string | undefined>;
  recordingIDs: string[];
};

export type Versioned<T> = T & { revision: number };
export function unpack<T>(record: OperationDocument): Versioned<T> {
  return { ...JSON.parse(record.body), revision: record.revision };
}
export function downloadText(name: string, text: string, type = "text/plain") {
  const url = URL.createObjectURL(new Blob([text], { type }));
  const anchor = document.createElement("a"); anchor.href = url; anchor.download = name; anchor.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
