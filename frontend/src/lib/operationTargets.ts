// Selection intents contain identifiers only, never record bodies or executable actions.
export const operationTargetViews = {
  monitor: "uptime",
  incident: "incidents",
  deployment: "deployments",
  "scan-profile": "security-scans",
  "security-scan": "security-scans",
  runbook: "runbooks",
} as const;

export type OperationTargetKind = keyof typeof operationTargetViews;
export type OperationTargetView = typeof operationTargetViews[OperationTargetKind];
export type OperationTarget = { kind: OperationTargetKind; id: string };

export function operationTarget(kind: OperationTargetKind, id: unknown): OperationTarget | null {
  if (!Object.hasOwn(operationTargetViews, kind) || typeof id !== "string" || !id.trim() || id !== id.trim()) return null;
  return { kind, id };
}

export function createOperationTargetInbox() {
  let pending: OperationTarget | null = null;
  return {
    request(kind: OperationTargetKind, id: unknown) {
      pending = operationTarget(kind, id);
      return pending ? operationTargetViews[pending.kind] : null;
    },
    take(view: OperationTargetView): OperationTarget | null {
      if (!pending || operationTargetViews[pending.kind] !== view) return null;
      const target = pending;
      pending = null; // Consume before loading; errors and missing records never retry later.
      return target;
    },
    discardOutside(view: string) {
      if (pending && operationTargetViews[pending.kind] !== view) pending = null;
    },
  };
}

// An envelope/body mismatch must never select a different persisted record.
export function matchingOperationID(envelopeID: unknown, bodyID: unknown): envelopeID is string {
  return typeof envelopeID === "string" && !!envelopeID.trim() && envelopeID === envelopeID.trim() && envelopeID === bodyID;
}

export function selectableOperationRecords<T extends { id: string }>(records: { id: string; body: string; revision: number }[] | null | undefined): (T & { revision: number })[] {
  return (records ?? []).flatMap(record => {
    try {
      const value = JSON.parse(record.body);
      return value && matchingOperationID(record.id, value.id) ? [{ ...value, revision: record.revision } as T & { revision: number }] : [];
    } catch { return []; }
  });
}
