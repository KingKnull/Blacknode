const mac = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform);

export const APP_SHORTCUTS = {
  newTab: mac ? ["⌘", "T"] : ["Ctrl", "Shift", "T"],
  closeTab: mac ? ["⌘", "W"] : ["Ctrl", "Shift", "W"],
  palette: [mac ? "⌘" : "Ctrl", "K"],
  ai: [mac ? "⌘" : "Ctrl", "Shift", "I"],
  focus: ["Ctrl", "Shift", "Z"],
  sidebar: ["Ctrl", "Shift", "H"],
};

export function shortcutLabel(action: keyof typeof APP_SHORTCUTS): string {
  return APP_SHORTCUTS[action].join("+");
}
