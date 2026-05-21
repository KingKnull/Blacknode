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
  el.style.width = window.innerWidth + "px";
  el.style.height = window.innerHeight + "px";
}
fitRoot();
window.addEventListener("resize", fitRoot);

const app = mount(App, {
  target: document.getElementById("app")!,
});

export default app;
