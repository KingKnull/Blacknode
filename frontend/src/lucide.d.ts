// @lucide/svelte@1.11.0 ships its JS but no .d.ts files, even though its
// package.json `exports` map points at declarations that don't exist on disk.
// Each named export is a Svelte icon component; TS can't type arbitrary named
// exports individually, so we declare the module ambiently until upstream
// ships types. Removes the "implicitly has an 'any' type" error project-wide.
declare module "@lucide/svelte";
