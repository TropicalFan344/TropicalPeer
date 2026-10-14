import { defineConfig, type Plugin } from 'vite'
import { readFileSync } from 'node:fs'

// The page imports the app's language files (locales/*.json) but needs only
// their "page" section; hand it just that, so each language stays small.
function pageStringsOnly(): Plugin {
  return {
    name: 'page-strings-only',
    enforce: 'pre',
    load(id) {
      if (!/[\/]locales[\/][\w-]+\.json$/.test(id)) return
      const all = JSON.parse(readFileSync(id, 'utf8'))
      return JSON.stringify({ page: all.page })
    },
  }
}

// The page is served by the rendezvous server at /r/<link>; assets sit next
// to it under /r/, and the build is embedded into the server binary.
export default defineConfig({
  base: './',
  plugins: [pageStringsOnly()],
  build: {
    outDir: '../internal/server/webdist',
    emptyOutDir: true,
    assetsInlineLimit: 0,
  },
})
