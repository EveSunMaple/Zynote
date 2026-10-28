import {readFile} from 'node:fs/promises'
import {dirname, join, resolve} from 'node:path'
import {fileURLToPath} from 'node:url'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const contract = JSON.parse(await readFile(join(root, 'contract/actions.json'), 'utf8')) as {
 schema: number
 total: number
 actions: {action: string; source: string; phase: number; backend: string}[]
}
if (contract.schema !== 1 || !Array.isArray(contract.actions) || contract.total !== contract.actions.length)
 throw new Error('契约结构或总数不一致')
const names = new Set<string>()
for (const row of contract.actions) {
 if (!/^[A-Za-z0-9:_-]+$/.test(row.action)) throw new Error('契约 action 名称无效：' + row.action)
 if (names.has(row.action)) throw new Error('契约 action 重复：' + row.action)
 if (!['go', 'shell'].includes(row.backend)) throw new Error('契约 backend 无效：' + row.action)
 if (!Number.isInteger(row.phase) || row.phase < 0 || row.phase > 6) throw new Error('契约 phase 无效：' + row.action)
 names.add(row.action)
}

const mainSource = await readFile(join(root, 'src/main/main.ts'), 'utf8')
const shellRegistered = new Set([...mainSource.matchAll(/case '([^']+)':/g)].map(match => match[1]))
const shellMissing = contract.actions
 .filter(row => row.backend === 'shell' && !shellRegistered.has(row.action))
 .map(row => row.action)
if (shellMissing.length) throw new Error('shell action 未在 Electron 壳登记：' + shellMissing.join(', '))

const goSource = await readFile(join(root, 'backend/internal/contract/contract.go'), 'utf8')
const capabilitiesMatch = /DefaultCapabilities\s*=\s*\[\]string\{([\s\S]*?)\n\}/.exec(goSource)
if (!capabilitiesMatch) throw new Error('无法解析 Go capabilities')
const capabilities = [...capabilitiesMatch[1].matchAll(/"([^"]+)"/g)].map(match => match[1])
const duplicated = capabilities.filter((action, index) => capabilities.indexOf(action) !== index)
if (duplicated.length) throw new Error('Go capabilities 重复：' + duplicated.join(', '))
const internal = capabilities.filter(action => !names.has(action))
for (const action of capabilities) {
 const row = contract.actions.find(item => item.action === action)
 if (!row) continue
 if (row.backend !== 'go') throw new Error('shell action 不应声明为 Go capability：' + action)
}
console.log(
 `ok contract ${contract.total} actions, go ${capabilities.length} (${internal.length} internal), shell ${contract.actions.filter(row => row.backend === 'shell').length}`
)
