import {glob, readFile} from 'node:fs/promises'
import {dirname, join, resolve} from 'node:path'
import {fileURLToPath} from 'node:url'

const compareCodeUnit = (a: string, b: string) => Number(a > b) - Number(a < b)
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

const registered = new Set<string>()
for await (const file of glob(join(root, 'src/main/ipc/*.ts'))) {
 const source = await readFile(file, 'utf8')
 const match = /actions:\s*new Set\(\[(.*?)\]\)/s.exec(source)
 if (!match) continue
 for (const action of match[1].matchAll(/'([^']+)'/g)) registered.add(action[1])
}
const missing = [...registered].filter(action => !names.has(action)).sort(compareCodeUnit)
const extra = [...names].filter(action => !registered.has(action)).sort(compareCodeUnit)
if (missing.length || extra.length) {
 throw new Error('契约与 TS action 不一致\n未登记：' + missing.join(', ') + '\n多余：' + extra.join(', '))
}

const goSource = await readFile(join(root, 'backend/internal/contract/contract.go'), 'utf8')
const capabilitiesMatch = /DefaultCapabilities\s*=\s*\[\]string\{([\s\S]*?)\n\}/.exec(goSource)
if (!capabilitiesMatch) throw new Error('无法解析 Go capabilities')
const capabilities = [...capabilitiesMatch[1].matchAll(/"([^"]+)"/g)].map(match => match[1])
const duplicated = capabilities.filter((action, index) => capabilities.indexOf(action) !== index)
if (duplicated.length) throw new Error('Go capabilities 重复：' + duplicated.join(', '))
const internal = capabilities.filter(action => !registered.has(action))
for (const action of capabilities) {
 if (!registered.has(action)) continue
 const row = contract.actions.find(item => item.action === action)
 if (!row) throw new Error('Go capability 不在契约中：' + action)
 if (row.backend !== 'go') throw new Error('shell action 不应声明为 Go capability：' + action)
}
console.log(
 `ok contract ${contract.total} actions, go ${capabilities.length} (${internal.length} internal), shell ${contract.actions.filter(row => row.backend === 'shell').length}`
)
