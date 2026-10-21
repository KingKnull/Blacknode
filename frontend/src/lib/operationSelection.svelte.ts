import { untrack } from "svelte";
import { app } from "./state.svelte";
import { createOperationTargetInbox, type OperationTarget, type OperationTargetKind, type OperationTargetView } from "./operationTargets";

const inbox = createOperationTargetInbox();
let requestVersion = $state(0);

export function openOperationTarget(kind: OperationTargetKind, id: string) {
  const view = inbox.request(kind, id);
  if (!view) return;
  requestVersion++;
  app.view = view;
}

// Called by the always-mounted palette, including while a lazy panel is loading.
export function discardOperationTargetOutside(view: string) {
  inbox.discardOutside(view);
}

// Use during component initialization. Refresh on mount and on same-panel searches;
// only commit the latest response while this panel remains active.
export function useOperationTarget<T>(view: OperationTargetView, options: {
  load: () => Promise<T>;
  apply: (data: T, target: OperationTarget | null) => void;
  clear: () => void;
  selection: () => string;
  error: (message: string) => void;
}) {
  $effect(() => {
    if (app.view !== view) return;
    const version = requestVersion;
    let active = true;
    untrack(() => {
      const target = inbox.take(view);
      if (target) options.clear();
      const selection = options.selection();
      void options.load().then(data => {
        if (active && version === requestVersion && app.view === view && selection === options.selection()) options.apply(data, target);
      }).catch(error => {
        if (active && version === requestVersion && app.view === view && selection === options.selection()) options.error(String(error));
      });
    });
    return () => { active = false; };
  });
}
