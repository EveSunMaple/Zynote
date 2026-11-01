import {readFileSync} from 'node:fs'
import {resolve} from 'node:path'
import {runInNewContext} from 'node:vm'
import ts from 'typescript'
import {z} from 'zod'
import {expect, it, vi} from 'vitest'
import {playbackCloseSchema} from '@shared/managed-playback'

it('runs the actual shell router without app startup and leaves legacy media:open for Go', async () => {
 const source = readFileSync(resolve(import.meta.dirname, '../main.ts'), 'utf8')
 const ast = ts.createSourceFile('main.ts', source, ts.ScriptTarget.Latest, true)
 const declaration = ast.statements.find(
  node => ts.isFunctionDeclaration(node) && node.name?.text === 'invokeShellAction'
 )
 if (!declaration) throw new Error('shell router missing')
 // Compile only the actual repository function; the app/window/bootstrap is never evaluated.
 const code = ts.transpileModule(declaration.getText(ast), {
  compilerOptions: {target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS}
 }).outputText
 const unhandled = Symbol('unhandled')
 const invoke = runInNewContext(code + ';invokeShellAction', {unhandled, z, playbackCloseSchema})
 const media = {open: vi.fn(async () => ({opened: true})), close: vi.fn()}
 expect(await invoke({}, '', {}, media, 'media:open', {id: 'existing-download-job'})).toBe(unhandled)
 expect(media.open).not.toHaveBeenCalled()
 expect(await invoke({}, '', {}, media, 'managed-media:open', {videoTaskID: 'controlled'})).toEqual({opened: true})
 const contract = JSON.parse(readFileSync(resolve(import.meta.dirname, '../../../contract/actions.json'), 'utf8'))
 const rows = contract.actions as {action: string; backend: string}[]
 expect(new Set(rows.map(row => row.action)).size).toBe(rows.length)
 expect(rows.filter(row => row.action === 'media:open')).toEqual([expect.objectContaining({backend: 'go'})])
 expect(rows.filter(row => row.action === 'managed-media:open')).toEqual([expect.objectContaining({backend: 'shell'})])
})
