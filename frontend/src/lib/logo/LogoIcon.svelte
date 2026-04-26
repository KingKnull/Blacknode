<script lang="ts">
  // App-icon variant: same mark but inside a rounded-square dark frame.
  // Use this for square icon contexts (splash mark, tile, large display).
  // Use LogoMark for inline UI where the surrounding chrome already
  // provides the frame.
  type Props = { size?: number; glow?: boolean; rounded?: number };
  let { size = 64, glow = true, rounded = 14 }: Props = $props();

  const filterID = `bn-icon-glow-${crypto.randomUUID().slice(0, 8)}`;
</script>

<svg
  width={size}
  height={size}
  viewBox="0 0 64 64"
  xmlns="http://www.w3.org/2000/svg"
  aria-label="blacknode app icon"
>
  <rect
    width="64"
    height="64"
    rx={rounded}
    fill="var(--color-surface-1, #0d0d12)"
    stroke="var(--color-line-strong, #2a2a36)"
    stroke-width="1"
  />
  {#if glow}
    <defs>
      <filter id={filterID} x="-50%" y="-50%" width="200%" height="200%">
        <feGaussianBlur stdDeviation="2.4" />
      </filter>
    </defs>
    <circle
      cx="44"
      cy="32"
      r="6"
      fill="var(--color-accent, #22d3ee)"
      opacity="0.55"
      filter="url(#{filterID})"
    />
  {/if}

  <path
    d="M22 20 L44 32 L22 44"
    stroke="var(--color-accent, #22d3ee)"
    stroke-width="3.5"
    stroke-linecap="round"
    stroke-linejoin="round"
    fill="none"
  />
  <circle cx="22" cy="20" r="4" fill="var(--color-accent, #22d3ee)" />
  <circle cx="44" cy="32" r="5.5" fill="var(--color-accent, #22d3ee)" />
  <circle cx="22" cy="44" r="4" fill="var(--color-accent, #22d3ee)" />
</svg>
