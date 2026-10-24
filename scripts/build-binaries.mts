import {copyFile, mkdir} from 'node:fs/promises'
import {spawnSync} from 'node:child_process'
import {dirname, join, resolve} from 'node:path'
import {fileURLToPath} from 'node:url'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const extension = process.platform === 'win32' ? '.exe' : ''
const output = join(root, 'backend', 'bin')
await mkdir(output, {recursive: true})

const run = (command: string, args: string[], cwd: string) => {
 console.log('>', command, args.join(' '))
 const result = spawnSync(command, args, {cwd, stdio: 'inherit', env: process.env})
 if (result.status !== 0) process.exit(result.status ?? 1)
}

run(
 'go',
 ['build', '-trimpath', '-ldflags=-s -w', '-o', join('bin', 'zynote-core' + extension), './cmd/zynote-core'],
 join(root, 'backend')
)
run('cargo', ['build', '--release', '--manifest-path', join(root, 'native', 'Cargo.toml')], root)
const native = join(root, 'native', 'target', 'release', 'zynote-native' + extension)
await copyFile(native, join(output, 'zynote-native' + extension))
console.log(
 'backend binaries ready:',
 join(output, 'zynote-core' + extension),
 join(output, 'zynote-native' + extension)
)
