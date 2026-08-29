// Typed pub/sub event bus replacing window.dispatchEvent(CustomEvent) patterns.
// Provides compile-time safety for event names and payload types.

type EventMap = {
  'insert-into-active-terminal': string;
  'tile-active-hosts': void;
  'connect-host': { hostID: string };
  'connect-host-mosh': { hostID: string };
  // "Session X has a connect intent waiting." The intent itself lives in app
  // state (AppState.requestConnect) so it survives a pane that hasn't mounted
  // yet; this event only saves an already-live pane from having to poll.
  'connect-intent': { sessionID: string };
  'new-host': void;
  'tab-label': { tabID: string; label: string };
  // Emitted by Terminal when a session connects/disconnects; Workspace maps
  // the session to its tab and renames it (empty label = back to local-N).
  'session-label': { sessionID: string; label: string };
};

type Listener<T> = (data: T) => void;

class AppEventBus {
  private listeners = new Map<string, Set<Listener<any>>>();

  on<K extends keyof EventMap>(event: K, fn: Listener<EventMap[K]>): () => void {
    let set = this.listeners.get(event);
    if (!set) {
      set = new Set();
      this.listeners.set(event, set);
    }
    set.add(fn);
    return () => {
      set!.delete(fn);
      if (set!.size === 0) this.listeners.delete(event);
    };
  }

  emit<K extends keyof EventMap>(
    event: K,
    ...args: EventMap[K] extends void ? [] : [EventMap[K]]
  ): void {
    const set = this.listeners.get(event);
    if (!set) return;
    const data = args[0] as EventMap[K];
    for (const fn of set) {
      fn(data);
    }
  }
}

export const bus = new AppEventBus();
export type { EventMap };
