import {build} from 'esbuild'
import {spawnSync} from 'node:child_process'
import {createHash} from 'node:crypto'
import {copyFile, mkdir, readFile, realpath, stat, writeFile} from 'node:fs/promises'
import {dirname, join, resolve} from 'node:path'
import {fileURLToPath} from 'node:url'

// Explicit existing runtimes and caller-supplied fixtures only. No installation,
// downloads, transcription or external provider. Output must be a new directory.
const args = process.argv.slice(2)
const option = (name: string) => {
 const index = args.indexOf(name)
 return index < 0 ? undefined : args[index + 1]
}
const required = (name: string) => {
 const value = option(name)
 if (!value) throw new Error('Required: ' + name)
 return resolve(value)
}
const electron = required('--electron'),
 go = required('--go'),
 core = required('--core')
const fixture = required('--fixture'),
 srt = required('--srt'),
 output = required('--output')
for (const path of [electron, go, core, fixture, srt])
 if (!(await stat(path)).isFile()) throw new Error('Missing fixture/runtime')
if ((await stat(fixture)).size > 16 * 1024 * 1024 || (await stat(srt)).size > 4000000)
 throw new Error('Fixture too large')
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
await mkdir(output)
const canonical = await realpath(output)
await mkdir(join(canonical, 'bundle'))
await mkdir(join(canonical, 'backend', 'bin'), {recursive: true})
const seed = join(canonical, process.platform === 'win32' ? 'seed.exe' : 'seed')
const compiled = spawnSync(go, ['build', '-o', seed, './internal/videoppt/testdata/bound_acceptance_seed.go'], {
 cwd: join(root, 'backend'),
 encoding: 'utf8',
 windowsHide: true
})
if (compiled.status !== 0) throw new Error('Seed build failed: ' + compiled.stderr)
const benchmark = args.includes('--benchmark')
const seeded = spawnSync(
 seed,
 [
  join(canonical, 'library'),
  fixture,
  srt,
  ...(benchmark ? ['benchmark'] : []),
  ...(args.includes('--large-journal') ? ['large-journal'] : [])
 ],
 {
  encoding: 'utf8',
  windowsHide: true
 }
)
if (seeded.status !== 0) throw new Error('Retention seed failed: ' + seeded.stderr)
await writeFile(join(canonical, 'seed.json'), seeded.stdout)
await build({
 entryPoints: ['src/main/media/managed-playback.ts', 'src/main/media/media-protocol.ts', 'src/main/backend/go-core.ts'],
 bundle: true,
 platform: 'node',
 format: 'cjs',
 external: ['electron'],
 outdir: join(canonical, 'bundle'),
 absWorkingDir: root
})
await copyFile(
 core,
 join(canonical, 'backend', 'bin', process.platform === 'win32' ? 'zynote-core.exe' : 'zynote-core')
)
await copyFile(join(root, 'scripts/bound-media-electron-fixture.cjs'), join(canonical, 'main.cjs'))
const env: NodeJS.ProcessEnv = {
 ...process.env,
 ZYNOTE_MEDIA_SMOKE_CODEC: option('--codec') || 'caller-supplied',
 ZYNOTE_BOUND_BENCHMARK: benchmark ? '1' : '0'
}
delete env.ELECTRON_RUN_AS_NODE
const execution = spawnSync(electron, [join(canonical, 'main.cjs')], {
 env,
 encoding: 'utf8',
 windowsHide: true,
 timeout: 70000,
 maxBuffer: 1024 * 1024
})
await writeFile(join(canonical, 'process.log'), (execution.stdout || '') + (execution.stderr || ''))
const resultPath = join(canonical, 'result.json')
const result = JSON.parse(await readFile(resultPath, 'utf8')) as Record<string, unknown>
const hash = async (path: string) =>
 createHash('sha256')
  .update(await readFile(path))
  .digest('hex')
const sources = [
 'src/main/media/managed-playback.ts',
 'src/main/media/media-protocol.ts',
 'src/main/backend/go-core.ts',
 'src/shared/managed-playback.ts',
 'scripts/bound-media-electron-smoke.mts',
 'scripts/bound-media-electron-fixture.cjs',
 'backend/internal/videoppt/testdata/bound_acceptance_seed.go'
]
result.sourcesSHA256 = Object.fromEntries(
 await Promise.all(sources.map(async path => [path, await hash(join(root, path))]))
)
result.artifactsSHA256 = Object.fromEntries(
 await Promise.all(
  [
   electron,
   go,
   core,
   fixture,
   srt,
   seed,
   join(canonical, 'bundle/media/managed-playback.js'),
   join(canonical, 'bundle/media/media-protocol.js'),
   join(canonical, 'bundle/backend/go-core.js')
  ].map(async path => [path, await hash(path)])
 )
)
result.scope = benchmark
 ? 'controlled actual IPC/stream benchmark; synthetic padding is not real course accuracy'
 : 'existing media/SRT; local HTTP fixed evidence only; no real model understanding or ASR'
result.process = {status: execution.status, signal: execution.signal, error: execution.error?.message}
if (execution.error || execution.status !== 0) result.pass = false
await writeFile(resultPath, JSON.stringify(result, null, 2) + '\n')
console.log(JSON.stringify({evidence: resultPath, pass: result.pass, status: execution.status}))
if (result.pass !== true) process.exitCode = 1
