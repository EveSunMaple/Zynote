import {existsSync} from 'node:fs'
import {spawnSync} from 'node:child_process'
import {join} from 'node:path'

const output = join('backend', 'bin', 'zynote-core')
if (!existsSync(output)) {
 const result = spawnSync(
  'go',
  ['build', '-trimpath', '-ldflags=-s -w', '-o', join('bin', 'zynote-core'), './cmd/zynote-core'],
  {stdio: 'inherit', cwd: 'backend'}
 )
 process.exit(result.status ?? 1)
}
