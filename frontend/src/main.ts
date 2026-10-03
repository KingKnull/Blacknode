import { mount } from "svelte";
import "./app.css";
import App from "./App.svelte";

// Workaround for Wails v3 / webkit2gtk on Linux: the webview widget does not
// always resize to fill the OS window when maximized, so vw/vh stay frozen at
// the initial pixel size. Force the root element to always match the actual
// window dimensions and re-apply on every resize event.
function fitRoot() {
  const el = document.getElementById("app");
  if (!el) return;
  // A hidden/minimized webview can report 0×0; pinning to that would leave the
  // app invisible until the next resize event. Fall back to the CSS 100vw/100vh
  // sizing instead and let a real measurement take over later.
  if (window.innerWidth <= 0 || window.innerHeight <= 0) {
    el.style.width = "";
    el.style.height = "";
    return;
  }
  el.style.width = window.innerWidth + "px";
  el.style.height = window.innerHeight + "px";
}
fitRoot();
window.addEventListener("resize", fitRoot);
window.addEventListener("focus", fitRoot);
document.addEventListener("visibilitychange", () => {
  if (document.visibilityState === "visible") {
    fitRoot();
  }
});

const app = mount(App, {
  target: document.getElementById("app")!,
});

export default app;
