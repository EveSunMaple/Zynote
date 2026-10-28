import {createHash} from 'node:crypto'
import {cp, mkdtemp, rm, writeFile} from 'node:fs/promises'
import {createServer} from 'node:http'
import {tmpdir} from 'node:os'
import {dirname, join, resolve} from 'node:path'
import {_electron, type ElectronApplication} from 'playwright'

const appIndex = process.argv.indexOf('--app')
if (appIndex < 0 || !process.argv[appIndex + 1])
 throw new Error('Usage: node scripts/update-smoke.mts --app <packaged executable>')
if (process.platform !== 'darwin') throw new Error('Update smoke currently runs on macOS only')
const executable = resolve(process.argv[appIndex + 1])
const bundle = dirname(dirname(dirname(executable)))
const version = '0.0.2'
const archive = 'Zynote-' + version + '-lite-arm64.zip'
const bytes = Buffer.from('update smoke payload')
const sha512 = createHash('sha512').update(bytes).digest('base64')
const metadata = [
 `version: ${version}`,
 'files:',
 `  - url: ${archive}`,
 `    sha512: ${sha512}`,
 `    size: ${bytes.length}`,
 `path: ${archive}`,
 `sha512: ${sha512}`,
 `releaseDate: '${new Date().toISOString()}'`,
 ''
].join('\n')
const root = await mkdtemp(join(tmpdir(), 'zynote-update-smoke-'))
const copied = join(root, 'Zynote.app')
const data = join(root, 'data')
let application: ElectronApplication | undefined
const server = createServer((request, response) => {
 const pathname = new URL(request.url ?? '/', 'http://127.0.0.1').pathname
 if (pathname === '/latest-mac.yml') {
  response.writeHead(200, {'content-type': 'text/yaml'})
  response.end(metadata)
  return
 }
 if (pathname === '/' + archive) {
  response.writeHead(200, {'content-type': 'application/zip', 'content-length': bytes.length})
  response.end(bytes)
  return
 }
 response.writeHead(404)
 response.end()
})
try {
 await new Promise<void>(resolveServer => server.listen(0, '127.0.0.1', resolveServer))
 const port = (server.address() as {port: number}).port
 await cp(bundle, copied, {recursive: true, verbatimSymlinks: true})
 await writeFile(
  join(copied, 'Contents', 'Resources', 'app-update.yml'),
  `provider: generic\nurl: http://127.0.0.1:${port}/\nchannel: latest\nupdaterCacheDirName: zynote-update-smoke\n`
 )
 application = await _electron.launch({
  executablePath: join(copied, 'Contents', 'MacOS', 'Zynote'),
  env: {...process.env, LISTENING_ASSISTANT_DATA: data, ZYNOTE_UPDATE_AUTODOWNLOAD: '0'}
 })
 const page = await application.firstWindow({timeout: 45000})
 await page.waitForLoadState('domcontentloaded')
 const status = (await page.evaluate(() =>
  (
   window as typeof window & {assistant: {invoke: (action: string, args?: unknown) => Promise<unknown>}}
  ).assistant.invoke('update:check')
 )) as {phase?: string; version?: string}
 if (status?.phase !== 'available' || status.version !== version)
  throw new Error('unexpected update status: ' + JSON.stringify(status))
 console.log(JSON.stringify({status: 'passed', phase: status.phase, version: status.version}))
} finally {
 await application?.close().catch(() => {})
 server.close()
 await rm(root, {recursive: true, force: true})
}
