// Orientation describes the separator line, not the direction it moves.
export function separatorKeyValue(
  event: Pick<KeyboardEvent, "key" | "altKey" | "ctrlKey" | "metaKey">,
  orientation: "horizontal" | "vertical",
  value: number,
  min: number,
  max: number,
  step: number,
): number | undefined {
  if (event.altKey || event.ctrlKey || event.metaKey) return undefined;
  const decrease = orientation === "vertical" ? "ArrowLeft" : "ArrowUp";
  const increase = orientation === "vertical" ? "ArrowRight" : "ArrowDown";
  switch (event.key) {
    case "Home": return min;
    case "End": return max;
    case decrease: return Math.max(min, Math.min(max, value - step));
    case increase: return Math.max(min, Math.min(max, value + step));
    default: return undefined;
  }
}
