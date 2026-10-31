import {copyFile, mkdir, readdir} from 'node:fs/promises'
import {existsSync} from 'node:fs'
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

async function copyWindowsRuntime() {
 if (process.platform !== 'win32') return
 const names = ['msvcp140.dll', 'msvcp140_1.dll', 'vcruntime140.dll', 'vcruntime140_1.dll']
 const roots = [
  process.env.VCToolsRedistDir,
  'C:\\Program Files (x86)\\Microsoft Visual Studio\\2022\\BuildTools\\VC\\Redist\\MSVC',
  'C:\\Program Files\\Microsoft Visual Studio\\2022\\BuildTools\\VC\\Redist\\MSVC'
 ].filter((value): value is string => Boolean(value))
 for (const root of roots) {
  if (!existsSync(root)) continue
  const versions = (await readdir(root, {withFileTypes: true}))
   .filter(entry => entry.isDirectory())
   .map(entry => entry.name)
   .sort()
   .reverse()
  for (const version of versions) {
   const source = join(root, version, 'x64', 'Microsoft.VC143.CRT')
   if (!existsSync(join(source, names[0]))) continue
   for (const name of names) await copyFile(join(source, name), join(output, name))
   return
  }
 }
 console.warn('未找到 VC++ 运行库，跳过 app-local 运行库复制')
}

run(
 'go',
 ['build', '-trimpath', '-ldflags=-s -w', '-o', join('bin', 'zynote-core' + extension), './cmd/zynote-core'],
 join(root, 'backend')
)
run('cargo', ['build', '--release', '--manifest-path', join(root, 'native', 'Cargo.toml')], root)
const native = join(root, 'native', 'target', 'release', 'zynote-native' + extension)
await copyFile(native, join(output, 'zynote-native' + extension))
// ONNX Runtime's Windows distribution needs its companion DLL beside the helper.
const directML = join(dirname(native), 'DirectML.dll')
if (process.platform === 'win32' && existsSync(directML)) await copyFile(directML, join(output, 'DirectML.dll'))
await copyWindowsRuntime()
console.log(
 'backend binaries ready:',
 join(output, 'zynote-core' + extension),
 join(output, 'zynote-native' + extension)
)
