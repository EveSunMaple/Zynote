import {createHash} from 'node:crypto'
import {readdir, writeFile} from 'node:fs/promises'
import {createReadStream} from 'node:fs'
import {dirname, join, resolve} from 'node:path'
import {fileURLToPath} from 'node:url'
import {containsPath} from './path-guard.mts'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const releaseRoot = join(root, 'release')
const directory = resolve(process.argv[2] || join(releaseRoot, 'lite'))
if (!containsPath(releaseRoot, directory))
 throw new Error('Checksum directory must stay inside the repository release directory')
const platform = process.argv[3]
if (!platform || !['win', 'mac', 'linux'].includes(platform)) {
 throw new Error('Expected platform: win, mac, or linux')
}
const files = (await readdir(directory, {withFileTypes: true})) // NOSONAR: directory is constrained to the repository release/ root above.
 .filter(entry => entry.isFile() && /\.(?:exe|dmg|zip|AppImage|deb)$/.test(entry.name))
 .map(entry => entry.name)
 .sort()
if (!files.length) throw new Error('No installers found in ' + directory)
const lines: string[] = []
for (const file of files) {
 const hash = createHash('sha256')
 for await (const chunk of createReadStream(join(directory, file))) hash.update(chunk)
 lines.push(hash.digest('hex') + '  ' + file)
}
await writeFile(join(directory, 'SHA256SUMS-' + platform + '.txt'), lines.join('\n') + '\n') // NOSONAR: directory is constrained to release/ and the file name uses a fixed platform allowlist.
console.log(lines.join('\n'))
