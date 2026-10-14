import { init } from "ghostty-web";

let initialization: Promise<void> | undefined;

export function initializeTerminalEngine(): Promise<void> {
  initialization ??= init();
  return initialization;
}
