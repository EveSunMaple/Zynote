import {unified, type Plugin} from 'unified'
import remarkParse from 'remark-parse'

const proseParser = unified().use(remarkParse).freeze()
type MarkdownNode = {
 type: string
 position?: {start: {offset?: number}; end: {offset?: number}}
 children?: MarkdownNode[]
 alt?: string | null
 value?: string
 lang?: string | null
 meta?: string | null
}

/** CommonMark locates protected syntax, including nested code and link destinations.
 * The second parse normalizes prose only; persisted/copied messages stay untouched.
 */
export const remarkChatMath: Plugin = function () {
 const parse = this.parser
 if (!parse) throw new Error('Markdown parser unavailable')
 this.parser = (source, file) => {
  const baseline = proseParser.parse(source)
  const ranges: [number, number][] = []
  const imageAlts: (string | null | undefined)[] = []
  const visit = (node: MarkdownNode) => {
   if (
    ['code', 'inlineCode', 'link', 'linkReference', 'image', 'imageReference', 'definition', 'html'].includes(node.type)
   ) {
    const start = node.position?.start.offset,
     end = node.position?.end.offset
    if (start !== undefined && end !== undefined) ranges.push([start, end])
    return
   }
   for (const child of node.children || []) visit(child)
  }
  const collectAlts = (node: MarkdownNode) => {
   if (node.type === 'image' || node.type === 'imageReference') imageAlts.push(node.alt)
   for (const child of node.children || []) collectAlts(child)
  }
  collectAlts(baseline)
  visit(baseline)
  ranges.sort((a, b) => a[0] - b[0])
  let normalized = '',
   position = 0
  for (const [start, end] of ranges) {
   normalized += normalizeMathProse(source.slice(position, start)) + source.slice(start, end)
   position = end
  }
  normalized += normalizeMathProse(source.slice(position))
  const tree = parse(normalized, file)
  let imageIndex = 0
  const restoreAlts = (node: MarkdownNode) => {
   if (node.type === 'image' || node.type === 'imageReference') node.alt = imageAlts[imageIndex++]
   for (const child of node.children || []) restoreAlts(child)
  }
  restoreAlts(tree)
  renderInlineCodeMath(tree, body => {
   const fragment = parse(`$${body}$`, file) as MarkdownNode
   return fragment.children?.[0]?.children || []
  })
  renderMathFences(tree, normalized, body => (parse(`$$\n${body}\n$$`, file) as MarkdownNode).children || [])
  return tree
 }
}

function renderInlineCodeMath(tree: MarkdownNode, math: (body: string) => MarkdownNode[]) {
 const visit = (parent: MarkdownNode) => {
  if (!parent.children) return
  parent.children = parent.children.flatMap(node => {
   if (node.type !== 'inlineCode') {
    visit(node)
    return [node]
   }
   const value = node.value || ''
   const segment = readMathSegment([value], 0, false, new Map())
   if (!segment || isProgram(segment.body)) return [node]
   return math(segment.body)
  })
 }
 visit(tree)
}

/** Explicit formula lines in a closed fence are presentation math. Other code stays code. */
function renderMathFences(tree: MarkdownNode, source: string, math: (body: string) => MarkdownNode[]) {
 const visit = (parent: MarkdownNode) => {
  if (!parent.children) return
  parent.children = parent.children.flatMap(node => {
   if (node.type === 'code') return renderMathCode(node, source, math)
   visit(node)
   return [node]
  })
 }
 visit(tree)
}

function renderMathCode(node: MarkdownNode, source: string, math: (body: string) => MarkdownNode[]): MarkdownNode[] {
 const language = node.lang?.toLowerCase() || ''
 if (language && !['math', 'latex', 'tex', 'katex'].includes(language)) return [node]
 // Prevent rehype-katex's implicit language-math rendering while an input is incomplete.
 const literal = language === 'math' ? {...node, lang: 'text'} : node
 if (!isClosedFence(node, source)) return [literal]
 const value = node.value || ''
 if (!language && isProgram(value)) return [literal]
 return splitMathCode(literal, value, !!language, math)
}

function isClosedFence(node: MarkdownNode, source: string): boolean {
 const raw = source.slice(node.position?.start.offset, node.position?.end.offset)
 const lines = raw.split(/\r?\n/),
  opening = /^(`{3,}|~{3,})/.exec(lines[0] || '')
 if (!opening || lines.length < 2) return false
 let closing = lines.at(-1)?.trim() || ''
 while (closing.startsWith('>')) closing = closing.slice(1).trimStart()
 return new RegExp(`^${opening[1][0]}{${opening[1].length},}$`).test(closing)
}

function isProgram(value: string): boolean {
 const keywords = /^(?:const|let|var|import|export|from|def|class|function|return)\b/
 return value.split('\n').some(line => {
  const text = line.trim()
  return (
   keywords.test(text) || text.startsWith('//') || text.startsWith('#') || text.includes('=>') || text.endsWith(';')
  )
 })
}

function splitMathCode(node: MarkdownNode, value: string, explicit: boolean, math: (body: string) => MarkdownNode[]) {
 const content = value.split('\n'),
  result: MarkdownNode[] = [],
  pending: string[] = []
 const closers = indexClosingLines(content)
 const flush = () => {
  if (pending.some(line => line.trim())) result.push({...node, value: pending.join('\n'), position: undefined})
  pending.length = 0
 }
 let index = 0
 while (index < content.length) {
  const segment = readMathSegment(content, index, explicit, closers)
  if (!segment) {
   pending.push(content[index++])
   continue
  }
  flush()
  result.push(...math(segment.body))
  index = segment.next
 }
 if (!result.length && explicit && value.trim() && !/[$`]|\\[()[\]]/.test(value)) return math(value)
 flush()
 return result.length ? result : [node]
}

const mathDelimiters = [
 ['$$', '$$'],
 [String.raw`\[`, String.raw`\]`],
 [String.raw`\(`, String.raw`\)`],
 ['$', '$']
]
function indexClosingLines(content: string[]): Map<string, number[]> {
 const result = new Map<string, number[]>()
 for (const close of ['$$', String.raw`\]`]) {
  const positions: number[] = []
  let next = -1
  for (let index = content.length - 1; index >= 0; index--) {
   positions[index] = next
   if (content[index].trim() === close) next = index
  }
  result.set(close, positions)
 }
 return result
}
function readMathSegment(
 content: string[],
 index: number,
 explicit: boolean,
 closers: Map<string, number[]>
): {body: string; next: number} | undefined {
 const line = content[index].trim()
 for (const [open, close] of mathDelimiters) {
  if (!line.startsWith(open)) continue
  const body = singleLineFormula(line, open, close, explicit)
  if (body !== undefined) return {body, next: index + 1}
  const segment = multilineFormula(content, index, open, close, closers)
  if (segment) return segment
 }
 return undefined
}

function singleLineFormula(line: string, open: string, close: string, explicit: boolean): string | undefined {
 if (line.length <= open.length + close.length || !line.endsWith(close)) return undefined
 const candidate = line.slice(open.length, -close.length)
 if (!candidate.trim() || candidate.includes('$')) return undefined
 if (!explicit && /^\d+(?:[.,]\d+)?(?:\s|$)/.test(candidate)) return undefined
 return candidate
}

function multilineFormula(
 content: string[],
 index: number,
 open: string,
 close: string,
 closers: Map<string, number[]>
) {
 if ((open !== '$$' && open !== String.raw`\[`) || content[index].trim() !== open) return undefined
 const end = closers.get(close)?.[index] ?? -1
 return end > index + 1 ? {body: content.slice(index + 1, end).join('\n'), next: end + 1} : undefined
}

function normalizeMathProse(source: string): string {
 let result = '',
  position = 0
 const specials = /[\\$]/g
 const missingClosers = new Set<string>()
 while (position < source.length) {
  specials.lastIndex = position
  const next = specials.exec(source)
  if (!next) return result + source.slice(position)
  result += source.slice(position, next.index)
  position = next.index
  const rest = source.slice(position)
  if (rest[0] === '\\') {
   const delimiter = rest[1]
   if (delimiter === '(' || delimiter === '[') {
    const close = delimiter === '(' ? '\\)' : '\\]'
    const end = missingClosers.has(close) ? -1 : rest.indexOf(close, 2)
    if (end < 0) missingClosers.add(close)
    const body = end < 0 ? '' : rest.slice(2, end)
    if (body.trim()) {
     result += delimiter === '(' ? `$${body}$` : `\n$$\n${body}\n$$\n`
     position += end + 2
     continue
    }
   }
   result += rest.slice(0, 2)
   position += Math.min(2, rest.length)
   continue
  }
  if (rest[0] === '$') {
   const width = rest[1] === '$' ? 2 : 1
   let end = width
   while ((end = rest.indexOf('$'.repeat(width), end)) >= 0 && isEscaped(rest, end)) end += width
   const body = end < 0 ? '' : rest.slice(width, end)
   const valid =
    body.trim() && (width === 2 || (!/^\s|\s$/.test(body) && !/[\n$]/.test(body) && !/\d/.test(rest[end + 1] || '')))
   if (valid) {
    result += rest.slice(0, end + width)
    position += end + width
    continue
   }
   result += '\\$'.repeat(width)
   position += width
   continue
  }
  result += rest[0]
  position++
 }
 return result
}

function isEscaped(source: string, position: number): boolean {
 let slashes = 0
 while (position > 0 && source[--position] === '\\') slashes++
 return slashes % 2 === 1
}
