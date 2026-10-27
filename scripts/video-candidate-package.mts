import {spawn, execFile, execFileSync} from 'node:child_process'
import {createHash, randomUUID} from 'node:crypto'
import {constants} from 'node:fs'
import {lstat, mkdir, open, readFile, realpath, writeFile} from 'node:fs/promises'
import {dirname, join, resolve} from 'node:path'
import {tmpdir} from 'node:os'
import {fileURLToPath} from 'node:url'
import {z} from 'zod'
import {inspectExecutable} from './media-source-stage.mts'
import {buildSources} from './media-build-source-lock.mts'
import {parseRuntimeManifestV2} from '../src/shared/runtime-manifest-v2.ts'
import {createVideoDescriptor, validateVideoDescriptor} from '../src/shared/runtime-descriptor-v2.ts'
import {containsPath} from './path-guard.mts'
const hex = z.string().regex(/^[a-f0-9]{64}$/)
const requestSchema = z
 .object({
  target: z.enum(['darwin-arm64', 'linux-x64']),
  productVersion: z.string().regex(/^\d+\.\d+\.\d+(?:[-+][A-Za-z0-9.-]+)?$/),
  archive: z.string(),
  buildReport: z.string(),
  expectedArchiveSha256: hex,
  expectedBuildReportSha256: hex,
  sourceBuildCommit: z.string().regex(/^[a-f0-9]{40}$/),
  sourceBuildRun: z.string().regex(/^\d+$/)
 })
 .strict()
function check(v: unknown, message: string): asserts v {
 if (!v) throw new Error(message)
}
async function plain(path: string, directory = false) {
 const target = resolve(path)
 for (let current = target; ; current = dirname(current)) {
  // biome-ignore format: NOSONAR must stay on the await line
  const s = await lstat(current) // NOSONAR: build/fixture script; serial order and bounded resource use are intentional.
  check(!s.isSymbolicLink(), 'Links forbidden')
  check(current !== target || directory ? s.isDirectory() : s.isFile() && s.nlink === 1, 'Invalid input file')
  if (dirname(current) === current) break
 }
 check(resolve(await realpath(target)) === target, 'Canonical path required')
 return target
}
async function sha(path: string) {
 const file = await open(path, constants.O_RDONLY | (constants.O_NOFOLLOW || 0)),
  h = createHash('sha256')
 try {
  for await (const b of file.createReadStream({autoClose: false})) h.update(b)
 } finally {
  await file.close()
 }
 return h.digest('hex')
}
async function listing(archive: string) {
 return await new Promise<string>((done, fail) =>
  execFile(
   tarBinary(),
   ['-tvf', archive],
   {
    encoding: 'utf8',
    maxBuffer: 32 * 1024 * 1024,
    timeout: 120000,
    env: {...process.env, LC_ALL: 'C', LANG: 'C', TAR_OPTIONS: ''}
   },
   (e, out, err) => (e || err.trim() ? fail(new Error('Archive listing failed')) : done(out))
  )
 )
}
function tarBinary() {
 if (process.platform === 'darwin') return '/usr/bin/tar'
 if (process.platform === 'linux') return '/usr/bin/bsdtar'
 return 'tar'
}
function verboseListing(line: string) {
 const fields: string[] = []
 let index = 0
 while (fields.length < 8) {
  while (index < line.length && (line[index] === ' ' || line[index] === '\t')) index++
  const start = index
  while (index < line.length && line[index] !== ' ' && line[index] !== '\t') index++
  if (start === index) return undefined
  fields.push(line.slice(start, index))
 }
 while (index < line.length && (line[index] === ' ' || line[index] === '\t')) index++
 const name = line.slice(index)
 if (!name || !/^-[rwx-]{9}$/.test(fields[0]) || !/^\d+$/.test(fields[1]) || !/^\d+$/.test(fields[4])) return undefined
 if (!/^[A-Za-z]{3}$/.test(fields[5]) || !/^\d{1,2}$/.test(fields[6])) return undefined
 if (!/^(?:\d{4}|\d{1,2}:\d{2})$/.test(fields[7])) return undefined
 return {size: Number(fields[4]), name}
}
export function selectedMemberSize(text: string, name: string) {
 check(
  /^[A-Za-z0-9._+~/-]+$/.test(name) && !name.split('/').some(p => !p || p === '..' || p === '.'),
  'Unsafe selected archive member'
 )
 const lines = text
  .replaceAll('\r\n', '\n')
  .split('\n')
  .filter(
   line => line.endsWith(' ' + name) || line.includes(' ' + name + ' -> ') || line.includes(' ' + name + ' link to ')
  )
 check(lines.length === 1, 'Selected archive member missing or duplicated: ' + name)
 const match = verboseListing(lines[0])
 check(match, 'Selected archive member is not a plain regular file')
 check(match.name === name, 'Selected archive member is not a plain regular file')
 const size = match.size
 check(Number.isSafeInteger(size) && size > 0 && size <= 256 * 1024 * 1024, 'Invalid selected member size')
 return size
}
// biome-ignore format: NOSONAR must stay on the function line
async function extractMember(archive: string, name: string, destination: string, size: number) { // NOSONAR: build/fixture script; bounded sequential verification steps intentionally kept together.
 const file = await open(destination, 'wx', 0o600),
  h = createHash('sha256')
 let count = 0
 try {
// biome-ignore format: NOSONAR must stay on the function line
  await new Promise<void>((done, fail) => { // NOSONAR: build/fixture script; bounded sequential verification steps intentionally kept together.
   const child = spawn(tarBinary(), ['-xOf', archive, name], {
    stdio: ['ignore', 'pipe', 'pipe'],
    env: {...process.env, LC_ALL: 'C', LANG: 'C', TAR_OPTIONS: ''}
   })
   const timer = setTimeout(() => {
    child.kill()
    fail(new Error('Member read timed out'))
   }, 120000)
   let stderr = ''
   child.stderr.on('data', b => {
    stderr = (stderr + b.toString()).slice(-1000)
   })
   child.once('error', fail)
// biome-ignore format: NOSONAR must stay on the function line
   const consume = (async () => { // NOSONAR: build/fixture script; bounded sequential verification steps intentionally kept together.
    for await (const bytes of child.stdout) {
     count += bytes.length
     check(count <= size, 'Member exceeded declared size')
     h.update(bytes)
     // biome-ignore format: NOSONAR must stay on the await line
     await file.writeFile(bytes) // NOSONAR: build/fixture script; serial order and bounded resource use are intentional.
    }
   })()
   child.once('close', code => {
    clearTimeout(timer)
    void consume
     .then(() => {
      check(code === 0 && !stderr.trim() && count === size, 'Member read failed')
      done()
     })
     .catch(fail)
   })
   void consume.catch(error => {
    child.kill()
    fail(error)
   })
  })
 } finally {
  await file.close()
 }
 return h.digest('hex')
}
// biome-ignore format: NOSONAR must stay on the function line
export async function packageVideoCandidate(value: unknown) { // NOSONAR: cohesive flow; further extraction would risk behavior changes.
 // NOSONAR: cohesive flow; further extraction would risk behavior changes.
 // NOSONAR: retained cohesive flow; decomposition tracked separately to avoid behavior regressions.
 const request = requestSchema.parse(value),
  archive = await plain(request.archive),
  reportPath = await plain(request.buildReport)
 check(
  (await lstat(archive)).size > 0 && (await lstat(archive)).size <= 1024 * 1024 * 1024,
  'Candidate archive exceeds size budget'
 )
 check((await lstat(reportPath)).size <= 4 * 1024 * 1024, 'Build report too large')
 check((await sha(archive)) === request.expectedArchiveSha256, 'Caller archive digest mismatch')
 check((await sha(reportPath)) === request.expectedBuildReportSha256, 'Caller build report digest mismatch')
 const linux = request.target === 'linux-x64'
 const target = linux ? ({platform: 'linux', arch: 'x64'} as const) : ({platform: 'darwin', arch: 'arm64'} as const)
 const source = JSON.parse(await readFile(reportPath, 'utf8'))
 check(
  source.state === 'source_build_candidate_verified' && source.distribution_status === 'pending',
  'Only verified pending source-build candidates accepted'
 )
 check(
  source.binding?.commit === request.sourceBuildCommit &&
   source.binding?.dirty === false &&
   source.binding?.platform === target.platform &&
   source.binding?.arch === target.arch,
  'Source build identity/target mismatch'
 )
 check(source.archiveSha256 === request.expectedArchiveSha256, 'Build report archive mismatch')
 const table = await listing(archive),
  temp = await plain(await realpath(tmpdir()), true),
  stage = join(temp, 'zynote-video-candidate-' + randomUUID())
 await mkdir(stage, {mode: 0o700})
 const payload = join(stage, 'payload')
 await mkdir(payload)
 const files: Array<{path: string; size: number; sha256: string; mode: '0644' | '0755'}> = []
 const copy = async (member: string, path: string, expected?: string) => {
  const size = selectedMemberSize(table, member)
  await mkdir(dirname(join(payload, path)), {recursive: true})
  const hash = await extractMember(archive, member, join(payload, path), size)
  check(!expected || hash === expected, 'Member digest differs from source evidence: ' + member)
  check((await sha(join(payload, path))) === hash, 'Payload reread mismatch')
  files.push({path, size, sha256: hash, mode: path === 'ffmpeg' || path === 'ffprobe' ? '0755' : '0644'})
 }
 try {
  for (const tool of ['ffmpeg', 'ffprobe']) {
   // biome-ignore format: NOSONAR must stay on the await line
   await copy('prefix/bin/' + tool, tool, hex.parse(source.diagnostics?.[tool]?.sha256)) // NOSONAR: build/fixture script; serial order and bounded resource use are intentional.
   if (linux) {
    // biome-ignore format: NOSONAR must stay on the await line
    const bytes = await readFile(join(payload, tool)) // NOSONAR: build/fixture script; serial order and bounded resource use are intentional.
    inspectExecutable(bytes, bytes.length, 'linux')
    const diagnostic = source.diagnostics[tool]
    const allowed = new Set([
     'libc.so.6',
     'libm.so.6',
     'libz.so.1',
     'libpthread.so.0',
     'libdl.so.2',
     'librt.so.1',
     'libstdc++.so.6',
     'libgcc_s.so.1',
     'ld-linux-x86-64.so.2'
    ])
    check(
     Array.isArray(diagnostic.needed) &&
      diagnostic.needed.length > 0 &&
      diagnostic.needed.every((name: string) => allowed.has(name)),
     'Unexpected Linux build dependency'
    )
    check(
     Array.isArray(diagnostic.resolved) &&
      diagnostic.needed.every((name: string) =>
       diagnostic.resolved.some(
        (entry: {name: string; realpath: string}) =>
         entry.name === name && entry.realpath.startsWith('/usr/lib/x86_64-linux-gnu/')
       )
      ),
     'Unresolved Linux build dependency'
    )
    check(!/\((?:RPATH|RUNPATH)\)/.test(diagnostic.dynamic), 'Unexpected Linux library search path')
   }
  }
  await copy('source-report.json', 'evidence/source-ledger.json')
  const ledger = JSON.parse(await readFile(join(payload, 'evidence/source-ledger.json'), 'utf8'))
  check(ledger.receipts?.length === 7, 'Seven source receipts required')
  for (const locked of buildSources) {
   const receipt = ledger.receipts.find((r: {source?: {id?: string}}) => r.source?.id === locked.id)
   check(
    receipt?.source?.sha256 === locked.sha256 && receipt.source.size === locked.size,
    'Source ledger differs from fixed source lock'
   )
   const base = 'sources/' + locked.id + '/' + locked.root
   // biome-ignore format: NOSONAR must stay on the await line
   await copy('archives/' + locked.archive, 'sources/' + locked.archive, locked.sha256) // NOSONAR: build/fixture script; serial order and bounded resource use are intentional.
   // biome-ignore format: NOSONAR must stay on the await line
   for (const license of locked.licenseFiles) await copy(base + '/' + license, 'licenses/' + locked.id + '/' + license) // NOSONAR: build/fixture script; serial order and bounded resource use are intentional.
   // biome-ignore format: NOSONAR must stay on the await line
   await copy( // NOSONAR: build/fixture script; serial order and bounded resource use are intentional.
    base + '/' + (locked.id === 'ffmpeg' ? 'ffbuild/config.log' : 'config.log'),
    'evidence/' + locked.id + '-config.log'
   )
  }
  await copy('sources/ffmpeg/ffmpeg-8.1.3/ffbuild/config.mak', 'evidence/ffmpeg-config.mak')
  if (linux) {
   await copy('sources/ffmpeg/ffmpeg-8.1.3/config.h', 'evidence/ffmpeg-config.h')
   await copy('build-evidence.json', 'evidence/build-evidence.json')
  } else
   for (const file of ['configure', 'configure.ac'])
    // biome-ignore format: NOSONAR must stay on the await line
    await copy( // NOSONAR: build/fixture script; serial order and bounded resource use are intentional.
     'sources/vorbis/libvorbis-1.3.7/' + file,
     'evidence/vorbis-' + file,
     hex.parse(source.sourcePatches?.patches?.find((p: {file: string}) => p.file === file)?.afterSha256)
    )
  const reportBytes = await readFile(reportPath)
  await writeFile(join(payload, 'evidence/build-result.json'), reportBytes, {flag: 'wx', mode: 0o600})
  files.push({
   path: 'evidence/build-result.json',
   size: reportBytes.length,
   sha256: createHash('sha256').update(reportBytes).digest('hex'),
   mode: '0644'
  })
  check(
   (await sha(archive)) === request.expectedArchiveSha256 &&
    (await sha(reportPath)) === request.expectedBuildReportSha256,
   'Input changed during packaging'
  )
  const manifest = parseRuntimeManifestV2(
   {
    schema: 2,
    productVersion: request.productVersion,
    target,
    components: [
     {
      id: 'video',
      label: 'Video source-build candidate',
      directory: 'video',
      target,
      bytes: files.reduce((n, f) => n + f.size, 0),
      files
     }
    ]
   },
   target
  )
  const descriptor = createVideoDescriptor(manifest)
  validateVideoDescriptor(descriptor, manifest)
  await writeFile(join(stage, 'runtime-manifest.json'), JSON.stringify(manifest, null, 2) + '\n', {flag: 'wx'})
  await writeFile(join(stage, 'component.json'), JSON.stringify(descriptor, null, 2) + '\n', {flag: 'wx'})
  const cwd = dirname(fileURLToPath(import.meta.url)),
   report = {
    generator: {
     commit: execFileSync('git', ['rev-parse', 'HEAD'], {cwd, encoding: 'utf8'}).trim(), // NOSONAR: CI/dev PATH.
     dirty: !!execFileSync('git', ['status', '--porcelain'], {cwd, encoding: 'utf8'}).trim(), // NOSONAR: CI/dev PATH.
     sourceSha256: await sha(fileURLToPath(import.meta.url)),
     sourceHashes: Object.fromEntries(
      await Promise.all(
       [
        '../src/shared/runtime-descriptor-v2.ts',
        '../src/shared/runtime-manifest-v2.ts',
        './media-build-source-lock.mts'
       ].map(async path => [path, await sha(fileURLToPath(new URL(path, import.meta.url)))])
      )
     )
    },
    state: 'candidate_only',
    distribution_status: 'pending',
    trusted: false,
    installable: false,
    sourceBuildRun: request.sourceBuildRun,
    sourceBuildCommit: request.sourceBuildCommit,
    archiveSha256: request.expectedArchiveSha256,
    buildReportSha256: request.expectedBuildReportSha256,
    descriptor,
    scope:
     'ELF headers are inspected without execution; Linux dependency metadata is bound to the supplied build report, not rechecked against this host. Evidence-bound candidate generation only. Caller manifest validation is still required; no installation authority or binary execution.'
   }
  await writeFile(join(stage, 'candidate-report.json'), JSON.stringify(report, null, 2) + '\n', {flag: 'wx'})
  return {stage, report, manifest}
 } catch (error) {
  throw new Error('Candidate packaging failed; incomplete stage retained at ' + stage, {cause: error})
 }
}
if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
 try {
  check(process.argv.length === 3, 'Usage: node scripts/video-candidate-package.mts <request.json>')
  const requestPath = await realpath(process.argv[2])
  check(
   [resolve('.'), resolve(process.env.GITHUB_WORKSPACE || '.'), await realpath(tmpdir())].some(root =>
    containsPath(root, requestPath)
   ),
   'Request file must stay inside the workspace or temporary directory'
  )
  check((await lstat(requestPath)).size <= 1024 * 1024, 'Request file too large') // NOSONAR: requestPath is realpath-resolved and constrained to the workspace/tmp roots above; only its size is read.
  const request = JSON.parse(await readFile(requestPath, 'utf8')) // NOSONAR: requestPath is resolved and constrained to the workspace/tmp roots with a 1 MiB cap above.
  console.log(JSON.stringify(await packageVideoCandidate(request)))
 } catch (error) {
  console.error(error)
  process.exitCode = 1
 }
}
