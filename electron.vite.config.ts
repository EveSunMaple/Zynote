import {resolve} from 'node:path'
import {defineConfig, externalizeDepsPlugin} from 'electron-vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import type {Plugin} from 'vite'
const sharedAlias = {'@shared': resolve('src/shared')}
// Vite injects <style> tags and talks to its HMR server; production keeps the strict CSP from index.html.
const devCsp: Plugin = {
 name: 'zynote-dev-csp',
 transformIndexHtml(html, context) {
  if (!context.server) return html
  const content =
   "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; media-src 'self' lecture-media:; connect-src 'self' ws://localhost:* http://localhost:* http://127.0.0.1:*"
  return html.replace(
   /(<meta http-equiv="Content-Security-Policy" content=")[^"]*(")/,
   (_, open: string, close: string) => open + content + close
  )
 }
}
const mainAlias = {...sharedAlias, '@main': resolve('src/main')}
const rendererAlias = {...sharedAlias, '@renderer': resolve('src/renderer/src')}
// Reduced from ElecKoi's Electron Vite configuration (AGPL-3.0).
export default defineConfig({
 main: {
  resolve: {alias: mainAlias},
  plugins: [externalizeDepsPlugin()],
  build: {
   lib: {entry: resolve('src/main/main.ts')}
  }
 },
 preload: {
  resolve: {alias: sharedAlias},
  plugins: [externalizeDepsPlugin()],
  build: {
   lib: {entry: resolve('src/preload/preload.ts')},
   rollupOptions: {output: {format: 'cjs', entryFileNames: '[name].cjs'}}
  }
 },
 renderer: {plugins: [react(), tailwindcss(), devCsp], resolve: {alias: rendererAlias}}
})
