<script lang="ts">
  import { onMount, tick } from "svelte";

  type Props = {
    onclose: () => void;
    /** Accessible name. Ignored when labelledby is set. */
    label?: string;
    /** id of an element inside the dialog that names it. */
    labelledby?: string;
    /** Click on the backdrop closes the dialog. Default true. */
    closeOnBackdrop?: boolean;
    /** Utility classes for the full-screen backdrop layer. */
    backdropClass?: string;
    /** Utility classes for the dialog panel. */
    panelClass?: string;
    /** Inline style for the dialog panel (shadows etc). */
    panelStyle?: string;
    children: any;
  };
  let {
    onclose,
    label,
    labelledby,
    closeOnBackdrop = true,
    backdropClass = "bg-black/80",
    panelClass = "",
    panelStyle = "",
    children,
  }: Props = $props();

  let panelEl: HTMLElement | undefined = $state();
  let prevFocus: HTMLElement | null = null;

  function focusables(): HTMLElement[] {
    if (!panelEl) return [];
    const sel =
      'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';
    return Array.from(panelEl.querySelectorAll<HTMLElement>(sel)).filter(
      (el) => el.offsetParent !== null,
    );
  }

  function onKeydown(e: KeyboardEvent) {
    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      onclose();
      return;
    }
    if (e.key !== "Tab") return;
    const items = focusables();
    if (items.length === 0) {
      e.preventDefault();
      panelEl?.focus();
      return;
    }
    const first = items[0];
    const last = items[items.length - 1];
    const active = document.activeElement as HTMLElement | null;
    if (e.shiftKey && (active === first || !panelEl?.contains(active))) {
      e.preventDefault();
      last.focus();
    } else if (!e.shiftKey && active === last) {
      e.preventDefault();
      first.focus();
    }
  }

  onMount(() => {
    prevFocus = document.activeElement as HTMLElement | null;
    void tick().then(() => {
      const auto = panelEl?.querySelector<HTMLElement>("[data-autofocus]");
      (auto ?? panelEl)?.focus();
    });
    return () => prevFocus?.focus?.();
  });
</script>

<svelte:window onkeydown={onKeydown} />

<div
  class="fixed inset-0 z-50 flex items-center justify-center {backdropClass}"
  role="presentation"
  onclick={(e) => {
    if (closeOnBackdrop && e.target === e.currentTarget) onclose();
  }}
>
  <div
    bind:this={panelEl}
    role="dialog"
    aria-modal="true"
    aria-label={labelledby ? undefined : label}
    aria-labelledby={labelledby}
    tabindex="-1"
    class="outline-none {panelClass}"
    style={panelStyle}
  >
    {@render children()}
  </div>
</div>
