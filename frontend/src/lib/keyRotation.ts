// Calendar dates are interpreted in the user's local day, never as local
// midnight timestamps (which would make DST change the reminder window).
export const ROTATION_SOON_DAYS = 30;
export type RotationStatus = "none" | "scheduled" | "soon" | "overdue";

export function rotationStatus(dueDate: string | undefined, now = new Date()): RotationStatus {
  if (!dueDate || !/^\d{4}-\d{2}-\d{2}$/.test(dueDate) || dueDate.startsWith("0000-")) return "none";
  const due = Date.parse(`${dueDate}T00:00:00Z`);
  if (!Number.isFinite(due) || new Date(due).toISOString().slice(0, 10) !== dueDate) return "none";
  const today = `${String(now.getFullYear()).padStart(4, "0")}-${String(now.getMonth() + 1).padStart(2, "0")}-${String(now.getDate()).padStart(2, "0")}`;
  const days = (due - Date.parse(`${today}T00:00:00Z`)) / 86_400_000;
  if (days < 0) return "overdue";
  return days <= ROTATION_SOON_DAYS ? "soon" : "scheduled";
}

// Notifications contain counts only: no names, IDs, fingerprints or key bytes.
export function rotationReminder(keys: ReadonlyArray<{ rotationDueDate?: string }>, now = new Date()): string | null {
  let overdue = 0;
  let soon = 0;
  for (const key of keys) {
    const status = rotationStatus(key.rotationDueDate, now);
    if (status === "overdue") overdue++;
    if (status === "soon") soon++;
  }
  if (!overdue && !soon) return null;
  return `${overdue} overdue · ${soon} due within ${ROTATION_SOON_DAYS} days. Review rotation dates in Keys. Reminders are advisory; authentication is unchanged.`;
}
