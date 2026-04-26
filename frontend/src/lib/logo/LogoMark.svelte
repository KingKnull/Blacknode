<script lang="ts">
  // The bare mark: three nodes forming a `>` chevron with two connecting
  // paths. Reads as both a terminal prompt and a small node graph. The
  // accent colour is taken from the `currentColor` of the parent — set it
  // via Tailwind text-* utilities or a wrapper style.
  type Props = { size?: number; glow?: boolean };
  let { size = 28, glow = true }: Props = $props();

  // Unique filter ID per instance so multiple marks on the same page don't
  // collide. SVG ids are document-scoped.
  const filterID = `bn-glow-${crypto.randomUUID().slice(0, 8)}`;
</script>

<svg
  width={size}
  height={size}
  viewBox="0 0 64 64"
  xmlns="http://www.w3.org/2000/svg"
  class="text-[var(--color-accent)]"
  aria-label="blacknode"
>
  {#if glow}
    <defs>
      <filter id={filterID} x="-50%" y="-50%" width="200%" height="200%">
        <feGaussianBlur stdDeviation="2.2" />
      </filter>
    </defs>
    <circle
      cx="44"
      cy="32"
      r="6"
      fill="currentColor"
      opacity="0.55"
      filter="url(#{filterID})"
    />
  {/if}

  <path
    d="M22 20 L44 32 L22 44"
    stroke="currentColor"
    stroke-width="3.5"
    stroke-linecap="round"
    stroke-linejoin="round"
    fill="none"
  />
  <circle cx="22" cy="20" r="4" fill="currentColor" />
  <circle cx="44" cy="32" r="5.5" fill="currentColor" />
  <circle cx="22" cy="44" r="4" fill="currentColor" />
</svg>
