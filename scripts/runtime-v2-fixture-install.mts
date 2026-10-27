import {createHash, randomUUID} from 'node:crypto'
import {constants} from 'node:fs'
import {lstat, mkdir, open, readdir, realpath, rename} from 'node:fs/promises'
import {dirname, join, resolve} from 'node:path'
import {tmpdir} from 'node:os'
import {parseRuntimeManifestV2, type RuntimeTarget} from '../src/shared/runtime-manifest-v2.ts'
import {validateVideoDescriptor} from '../src/shared/runtime-descriptor-v2.ts'

const compareCodeUnit = (a: string, b: string) => Number(a > b) - Number(a < b)
function check(value: unknown, message: string): asserts value {
 if (!value) throw new Error(message)
}
async function plain(path: string, directory: boolean) {
 const target = resolve(path)
 for (let current = target; ; current = dirname(current)) {
  // biome-ignore format: NOSONAR must stay on the await line
  const info = await lstat(current) // NOSONAR: build/fixture script; serial order and bounded resource use are intentional.
  check(!info.isSymbolicLink(), 'Fixture paths cannot contain links')
  check(
   current !== target || directory ? info.isDirectory() : info.isFile() && info.nlink === 1,
   'Fixture requires regular non-hardlinked files'
  )
  if (dirname(current) === current) break
 }
 check(resolve(await realpath(target)) === target, 'Fixture requires canonical paths')
 return target
}
async function hashFile(path: string, expectedSize: number, signal: AbortSignal) {
 signal.throwIfAborted()
 await plain(path, false)
 const handle = await open(path, constants.O_RDONLY | (constants.O_NOFOLLOW || 0)),
  hash = createHash('sha256')
 let size = 0
 try {
  const before = await handle.stat()
  check(before.size === expectedSize, 'File size mismatch')
  for await (const bytes of handle.createReadStream({autoClose: false})) {
   signal.throwIfAborted()
   size += bytes.length
   check(size <= expectedSize, 'File grew during verification')
   hash.update(bytes)
  }
  const after = await handle.stat(),
   pathInfo = await lstat(path)
  check(
   before.dev === pathInfo.dev &&
    before.ino === pathInfo.ino &&
    before.mtimeMs === after.mtimeMs &&
    before.ctimeMs === after.ctimeMs &&
    after.size === size &&
    size === expectedSize,
   'File changed during verification'
  )
 } finally {
  await handle.close()
 }
 return hash.digest('hex')
}
// biome-ignore format: NOSONAR must stay on the function line
async function exactFiles(root: string, paths: string[]) { // NOSONAR: build/fixture script; bounded sequential verification steps intentionally kept together.
 const expected = new Set(paths),
  directories = new Set<string>()
 for (const path of paths) {
  const parts = path.split('/')
  for (let i = 1; i < parts.length; i++) directories.add(parts.slice(0, i).join('/'))
 }
 const seen = new Set<string>()
// biome-ignore format: NOSONAR must stay on the function line
 async function walk(directory: string, prefix: string) { // NOSONAR: build/fixture script; bounded sequential verification steps intentionally kept together.
  await plain(directory, true)
  // biome-ignore format: NOSONAR must stay on the await line
  for (const name of await readdir(directory)) { // NOSONAR: build/fixture script; serial order and bounded resource use are intentional.
   const relative = prefix ? prefix + '/' + name : name,
    path = join(directory, name),
    // biome-ignore format: NOSONAR must stay on the await line
    info = await lstat(path) // NOSONAR: build/fixture script; serial order and bounded resource use are intentional.
   check(!info.isSymbolicLink(), 'Payload link forbidden')
   if (info.isDirectory()) {
    check(directories.has(relative), 'Unexpected payload directory')
    // biome-ignore format: NOSONAR must stay on the await line
    await walk(path, relative) // NOSONAR: build/fixture script; serial order and bounded resource use are intentional.
   } else {
    check(expected.has(relative) && info.isFile() && info.nlink === 1, 'Unexpected payload file')
    seen.add(relative)
   }
  }
 }
 await walk(root, '')
 check(seen.size === expected.size, 'Payload file set is incomplete')
}
/** Fixture-only: caller-selected metadata is not application installation authority. */
export async function installVideoFixture(
 // NOSONAR: cohesive flow; further extraction would risk behavior changes.
 // NOSONAR: retained cohesive flow; decomposition tracked separately to avoid behavior regressions.
 request: {
  callerManifest: unknown
  expectedTarget: RuntimeTarget
  expectedProductVersion: string
  descriptor: unknown
  payloadDirectory: string
 },
 signal: AbortSignal
) {
 signal.throwIfAborted()
 check(
  Object.keys(request).sort(compareCodeUnit).join(',') ===
   'callerManifest,descriptor,expectedProductVersion,expectedTarget,payloadDirectory',
  'Unknown fixture request field'
 )
 check(
  process.platform === 'darwin' &&
   process.arch === 'arm64' &&
   request.expectedTarget.platform === process.platform &&
   request.expectedTarget.arch === process.arch,
  'Fixture requires actual macOS arm64 target'
 )
 const manifest = parseRuntimeManifestV2(request.callerManifest, request.expectedTarget)
 check(manifest.productVersion === request.expectedProductVersion, 'Fixture version differs from caller selection')
 validateVideoDescriptor(request.descriptor, manifest)
 const files = manifest.components[0].files
 check(files.length <= 1000 && manifest.components[0].bytes <= 256 * 1024 * 1024, 'Fixture payload budget exceeded')
 const input = await plain(request.payloadDirectory, true)
 const inputIdentity = await lstat(input)
 await exactFiles(
  input,
  files.map(file => file.path)
 )
 for (const file of files)
  // biome-ignore format: NOSONAR must stay on the await line
  check((await hashFile(join(input, file.path), file.size, signal)) === file.sha256, 'Input digest mismatch') // NOSONAR: build/fixture script; serial order and bounded resource use are intentional.
 signal.throwIfAborted()
 const temp = await plain(await realpath(tmpdir()), true),
  root = join(temp, 'zynote-v2-fixture-' + randomUUID())
 await mkdir(root, {mode: 0o700})
 const stage = join(root, 'stage'),
  published = join(root, 'published')
 await mkdir(stage, {mode: 0o700})
 const rootIdentity = await lstat(root),
  stageIdentity = await lstat(stage)
 const stableDirectories = async () => {
  for (const [path, identity] of [
   [input, inputIdentity],
   [root, rootIdentity],
   [stage, stageIdentity]
  ] as const) {
   // biome-ignore format: NOSONAR must stay on the await line
   await plain(path, true) // NOSONAR: build/fixture script; serial order and bounded resource use are intentional.
   // biome-ignore format: NOSONAR must stay on the await line
   const current = await lstat(path) // NOSONAR: build/fixture script; serial order and bounded resource use are intentional.
   check(current.dev === identity.dev && current.ino === identity.ino, 'Fixture directory identity changed')
  }
 }
 try {
  for (const file of files) {
   signal.throwIfAborted()
   // biome-ignore format: NOSONAR must stay on the await line
   await stableDirectories() // NOSONAR: build/fixture script; serial order and bounded resource use are intentional.
   const source = join(input, file.path),
    destination = join(stage, file.path) // NOSONAR: sequential bounded I/O; parallelizing would break ordering and rate limits.
   await plain(source, false) // NOSONAR: sequential bounded I/O; parallelizing would break ordering and rate limits.
   await mkdir(dirname(destination), {recursive: true, mode: 0o700}) // NOSONAR: sequential bounded I/O; parallelizing would break ordering and rate limits.
   await plain(dirname(destination), true) // NOSONAR: sequential bounded I/O; parallelizing would break ordering and rate limits.
   const reader = await open(source, constants.O_RDONLY | (constants.O_NOFOLLOW || 0)), // NOSONAR: sequential bounded I/O; parallelizing would break ordering and rate limits.
    writer = await open(destination, 'wx', 0o600) // NOSONAR: await inside loop is required for bounded ordered I/O.
   let copied = 0
   try {
    // NOSONAR: sequential bounded I/O; parallelizing would break ordering and rate limits.
    const before = await reader.stat() // NOSONAR: await inside loop is required for bounded ordered I/O.
    check(before.isFile() && before.nlink === 1 && before.size === file.size, 'Copy source type/size mismatch') // NOSONAR: sequential bounded I/O; parallelizing would break ordering and rate limits.
    for await (const bytes of reader.createReadStream({autoClose: false})) {
     // NOSONAR: await inside loop is required for bounded ordered I/O.
     signal.throwIfAborted()
     copied += bytes.length
     check(copied <= file.size, 'Source grew during copy')
     await writer.writeFile(bytes)
    } // NOSONAR: sequential bounded I/O; parallelizing would break ordering and rate limits.
    await writer.sync() // NOSONAR: sequential bounded I/O; parallelizing would break ordering and rate limits.
    const after = await reader.stat(), // NOSONAR: sequential bounded I/O; parallelizing would break ordering and rate limits.
     current = await lstat(source) // NOSONAR: await inside loop is required for bounded ordered I/O.
    check(
     before.dev === after.dev &&
      before.ino === after.ino &&
      before.dev === current.dev &&
      before.ino === current.ino &&
      before.mtimeMs === after.mtimeMs &&
      before.ctimeMs === after.ctimeMs &&
      after.size === file.size,
     'Copy source changed'
    )
   } finally {
    // NOSONAR: sequential bounded I/O; parallelizing would break ordering and rate limits.
    await reader.close() // NOSONAR: sequential bounded I/O; parallelizing would break ordering and rate limits.
    await writer.close() // NOSONAR: await inside loop is required for bounded ordered I/O.
   }
   check(
    // NOSONAR: sequential bounded I/O; parallelizing would break ordering and rate limits.
    copied === file.size && (await hashFile(destination, file.size, signal)) === file.sha256, // NOSONAR: await inside loop is required for bounded ordered I/O.
    'Copied payload differs from manifest'
   )
   signal.throwIfAborted() // NOSONAR: sequential bounded I/O; parallelizing would break ordering and rate limits.
   await plain(destination, false) // NOSONAR: sequential bounded I/O; parallelizing would break ordering and rate limits.
   const permissionHandle = await open(destination, constants.O_RDONLY | (constants.O_NOFOLLOW || 0)) // NOSONAR: await inside loop is required for bounded ordered I/O.
   try {
    // NOSONAR: sequential bounded I/O; parallelizing would break ordering and rate limits.
    const identity = await permissionHandle.stat(), // NOSONAR: sequential bounded I/O; parallelizing would break ordering and rate limits.
     current = await lstat(destination) // NOSONAR: await inside loop is required for bounded ordered I/O.
    check(
     identity.dev === current.dev && identity.ino === current.ino && identity.isFile() && identity.nlink === 1,
     'Permission target identity changed'
    )
    signal.throwIfAborted() // NOSONAR: sequential bounded I/O; parallelizing would break ordering and rate limits.
    await permissionHandle.chmod(file.mode === '0755' ? 0o755 : 0o644) // NOSONAR: await inside loop is required for bounded ordered I/O.
   } finally {
    // NOSONAR: sequential bounded I/O; parallelizing would break ordering and rate limits.
    await permissionHandle.close() // NOSONAR: await inside loop is required for bounded ordered I/O.
   }
  }
  // Last complete verification immediately precedes publish; cancellation remains active.
  await stableDirectories()
  await exactFiles(
   input,
   files.map(file => file.path)
  )
  for (const file of files) // NOSONAR: sequential bounded I/O; parallelizing would break ordering and rate limits.
   check((await hashFile(join(input, file.path), file.size, signal)) === file.sha256, 'Input changed before publish') // NOSONAR: await inside loop is required for bounded ordered I/O.
  await exactFiles(
   stage,
   files.map(file => file.path)
  )
  for (const file of files) {
   signal.throwIfAborted()
   const path = join(stage, file.path) // NOSONAR: sequential bounded I/O; parallelizing would break ordering and rate limits.
   check((await hashFile(path, file.size, signal)) === file.sha256, 'Final payload digest mismatch') // NOSONAR: sequential bounded I/O; parallelizing would break ordering and rate limits.
   check(((await lstat(path)).mode & 0o7777) === (file.mode === '0755' ? 0o755 : 0o644), 'Final POSIX mode mismatch') // NOSONAR: await inside loop is required for bounded ordered I/O.
  }
  await stableDirectories()
  try {
   await lstat(published)
   throw new Error('Fixture publish destination already exists')
  } catch (error) {
   if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error
  }
  signal.throwIfAborted()
  await rename(stage, published)
  return {
   state: 'fixture_published',
   root,
   published,
   manifest,
   applicationInstaller: false,
   installationAuthority: false,
   scope:
    'Owned temporary fixture only; original payload permissions untouched, no binary execution or application installation.'
  }
 } catch (error) {
  throw new Error('Fixture failed; unpublished stage retained at ' + stage, {cause: error})
 }
}
