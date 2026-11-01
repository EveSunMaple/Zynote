import {build} from 'esbuild'
import {spawnSync} from 'node:child_process'
import {createHash} from 'node:crypto'
import {mkdtemp, realpath, mkdir, copyFile, readFile, writeFile, stat} from 'node:fs/promises'
import {tmpdir} from 'node:os'
import {dirname, join, resolve} from 'node:path'
import {fileURLToPath} from 'node:url'

// Explicit runtime and fixture only: no Electron installation, downloads, ASR or model calls.
const args = process.argv.slice(2)
function option(name: string) {
 const index = args.indexOf(name)
 return index < 0 ? undefined : args[index + 1]
}
const electron = option('--electron') || process.env.ZYNOTE_ELECTRON_BINARY
const fixture = option('--fixture')
if (!electron || !fixture)
 throw new Error(
  'Usage: node scripts/managed-media-electron-smoke.mts --electron <existing-executable> --fixture <small-video> [--codec <label>] [--output <new-directory>]'
 )
if (
 !(await stat(electron)).isFile() ||
 !(await stat(fixture)).isFile() ||
 (await stat(fixture)).size > 16 * 1024 * 1024
)
 throw new Error('Runtime/fixture unavailable or fixture exceeds 16MiB')
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const requested = option('--output')
const output = requested ? resolve(requested) : await mkdtemp(join(tmpdir(), 'zynote-media-smoke-'))
if (requested) await mkdir(output) // Never overwrite an existing evidence directory.
const canonical = await realpath(output)
await mkdir(join(canonical, 'bundle'))
await build({
 entryPoints: [join(root, 'src/main/media/managed-playback.ts'), join(root, 'src/main/media/media-protocol.ts')],
 bundle: true,
 platform: 'node',
 format: 'cjs',
 external: ['electron'],
 outdir: join(canonical, 'bundle'),
 absWorkingDir: root
})
await copyFile(join(root, 'scripts/managed-media-electron-fixture.cjs'), join(canonical, 'main.cjs'))
await copyFile(fixture, join(canonical, 'fixture.bin'))
const env: NodeJS.ProcessEnv = {
 ...process.env,
 ZYNOTE_MEDIA_SMOKE_CODEC: option('--codec') || 'caller-supplied fixture'
}
delete env.ELECTRON_RUN_AS_NODE
const execution = spawnSync(resolve(electron), [join(canonical, 'main.cjs')], {
 env,
 encoding: 'utf8',
 windowsHide: true,
 timeout: 40000,
 maxBuffer: 1024 * 1024
})
const resultPath = join(canonical, 'result.json')
const result = JSON.parse(await readFile(resultPath, 'utf8')) as Record<string, unknown>
const hash = (bytes: Uint8Array) => createHash('sha256').update(bytes).digest('hex')
result.runtimeSHA256 = hash(await readFile(electron))
result.sources = Object.fromEntries(
 await Promise.all(
  [
   'src/main/media/managed-playback.ts',
   'src/main/media/media-protocol.ts',
   'src/shared/managed-playback.ts',
   'scripts/managed-media-electron-fixture.cjs'
  ].map(async path => [path, hash(await readFile(join(root, path)))])
 )
)
result.process = {
 status: execution.status,
 signal: execution.signal,
 error: execution.error?.message,
 stderr: execution.stderr?.slice(-4000)
}
await writeFile(resultPath, JSON.stringify(result, null, 2) + '\n')
console.log(JSON.stringify({evidence: resultPath, pass: result.pass, status: execution.status}))
if (execution.error || execution.status !== 0 || result.pass !== true) process.exitCode = 1
