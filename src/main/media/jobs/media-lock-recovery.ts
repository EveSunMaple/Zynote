import {lstat, readFile, readdir, unlink} from 'node:fs/promises'
import {join} from 'node:path'
import {z} from 'zod'
const ownerSchema = z.object({
 version: z.literal(1),
 pid: z.number().int().positive().safe(),
 identity: z.string().min(1)
})
/** Called only after the worker holds its exclusive queue lease. Ambiguous locks are preserved. */
export async function recoverDownloadLocks(directory: string, identity: string) {
 const preserved: string[] = [],
  removed: string[] = []
 // biome-ignore format: NOSONAR must stay on the await line
 for (const name of await readdir(directory)) { // NOSONAR: ordered/limited operation; parallelizing would change atomicity, rate limits, or deterministic progress.
  if (!/^track-\d+(?:-segment-\d+)?(?:-chunk-\d+)?\.lock$/.test(name)) continue
  const path = join(directory, name),
   // biome-ignore format: NOSONAR must stay on the await line
   info = await lstat(path) // NOSONAR: ordered/limited operation; parallelizing would change atomicity, rate limits, or deterministic progress.
  if (info.isSymbolicLink() || !info.isFile()) {
   preserved.push(name)
   continue
  }
  let owner: z.infer<typeof ownerSchema>
  try {
   // biome-ignore format: NOSONAR must stay on the await line
   owner = ownerSchema.parse(JSON.parse(await readFile(path, 'utf8'))) // NOSONAR: ordered/limited operation; parallelizing would change atomicity, rate limits, or deterministic progress.
  } catch {
   preserved.push(name)
   continue
  }
  if (!owner.identity.startsWith(identity + ':')) {
   preserved.push(name)
   continue
  }
  let dead = false
  try {
   process.kill(owner.pid, 0)
  } catch (error) {
   dead = (error as NodeJS.ErrnoException).code === 'ESRCH'
  }
  if (!dead) {
   preserved.push(name)
   continue
  }
  // biome-ignore format: NOSONAR must stay on the await line
  await unlink(path) // NOSONAR: ordered/limited operation; parallelizing would change atomicity, rate limits, or deterministic progress.
  removed.push(name)
 }
 return {removed, preserved}
}
