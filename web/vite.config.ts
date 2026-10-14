import { defineConfig } from 'vite'

// The page is served by the rendezvous server at /r/<link>; assets sit next
// to it under /r/, and the build is embedded into the server binary.
export default defineConfig({
  base: './',
  build: {
    outDir: '../internal/server/webdist',
    emptyOutDir: true,
    assetsInlineLimit: 0,
  },
})
