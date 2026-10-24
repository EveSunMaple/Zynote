import {existsSync} from 'node:fs'
import {spawnSync} from 'node:child_process'
import {join} from 'node:path'

const extension = process.platform === 'win32' ? '.exe' : ''
const core = join('backend', 'bin', 'zynote-core' + extension)
const native = join('native', 'target', 'release', 'zynote-native' + extension)
if (!existsSync(core) || !existsSync(native)) {
 const result = spawnSync('node', ['scripts/build-binaries.mts'], {stdio: 'inherit'})
 process.exit(result.status ?? 1)
}
