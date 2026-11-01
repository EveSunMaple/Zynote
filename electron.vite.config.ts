import {resolve} from 'node:path'
import {defineConfig, externalizeDepsPlugin} from 'electron-vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import type {Plugin} from 'vite'
const sharedAlias = {'@shared': resolve('src/shared')}
// The development server resolves PDF assets and the HMR address at startup.
const devCsp: Plugin = {
 name: 'zynote-dev-csp',
 config(_, {command}) {
  if (command !== 'serve') return
  // Use the workspace path even when pnpm resolves the package through a junction on a different drive.
  const assets = resolve('node_modules/pdfjs-dist').replaceAll('\\', '/')
  return {define: {'import.meta.env.VITE_TEXTBOOK_PDF_ASSETS': JSON.stringify(`/@fs/${assets}/`)}}
 },
 transformIndexHtml(html, context) {
  if (!context.server) return html
  // Only add the listening Vite server's WebSocket origins for hot reload.
  const sockets = context.server.resolvedUrls?.local.map(url => new URL(url.replace(/^http/, 'ws')).origin) ?? []
  return html
   .replace("connect-src 'self'", ["connect-src 'self'", ...sockets].join(' '))
   .replace("style-src 'self';", "style-src 'self' 'unsafe-inline';")
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
