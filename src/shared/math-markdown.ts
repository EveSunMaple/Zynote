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
  renderMathFences(tree, normalized, body => (parse(`$$\n${body}\n$$`, file) as MarkdownNode).children || [])
  return tree
 }
}

/** Explicit formula lines in a closed fence are presentation math. Other code stays code. */
function renderMathFences(tree: MarkdownNode, source: string, math: (body: string) => MarkdownNode[]) {
 const visit = (parent: MarkdownNode) => {
  if (!parent.children) return
  parent.children = parent.children.flatMap(node => {
   if (node.type !== 'code') {
    visit(node)
    return [node]
   }
   const language = node.lang?.toLowerCase() || ''
   if (language && !['math', 'latex', 'tex', 'katex'].includes(language)) return [node]
   const literal = language === 'math' ? {...node, lang: 'text'} : node
   const raw = source.slice(node.position?.start.offset, node.position?.end.offset)
   const lines = raw.split(/\r?\n/),
    opening = lines[0]?.match(/^(`{3,}|~{3,})/)
   const closing = lines
    .at(-1)
    ?.replace(/^(?:[ \t]*>[ \t]?)+/, '')
    .trim()
   if (
    !opening ||
    lines.length < 2 ||
    !closing ||
    !new RegExp(`^${opening[1][0]}{${opening[1].length},}$`).test(closing)
   )
    return [literal]
   const value = node.value || ''
   // Unlabeled programs may contain formula-shaped strings; explicit TeX labels express math intent.
   if (
    !language &&
    /^\s*(?:const\b|let\b|var\b|import\b|export\b|from\b|def\b|class\b|function\b|return\b|\/\/|#)|=>|;\s*$/m.test(
     value
    )
   )
    return [literal]
   const content = value.split('\n'),
    result: MarkdownNode[] = [],
    pending: string[] = []
   const flush = () => {
    if (pending.some(line => line.trim())) result.push({...literal, value: pending.join('\n'), position: undefined})
    pending.length = 0
   }
   for (let index = 0; index < content.length; index++) {
    const line = content[index].trim()
    let body: string | undefined
    for (const [open, close] of [
     ['$$', '$$'],
     ['\\[', '\\]'],
     ['\\(', '\\)'],
     ['$', '$']
    ]) {
     if (!line.startsWith(open)) continue
     if (line.length > open.length + close.length && line.endsWith(close)) {
      const candidate = line.slice(open.length, -close.length)
      if (candidate.trim() && !candidate.includes('$') && (language || !/^\d+(?:[.,]\d+)?(?:\s|$)/.test(candidate)))
       body = candidate
     } else if ((open === '$$' || open === '\\[') && line === open) {
      const end = content.findIndex((part, at) => at > index && part.trim() === close)
      if (end > index + 1) {
       body = content.slice(index + 1, end).join('\n')
       index = end
      }
     }
     if (body !== undefined) break
    }
    if (body !== undefined) {
     flush()
     result.push(...math(body))
    } else pending.push(content[index])
   }
   if (!result.length && language && value.trim() && !/[$`]|\\[()[\]]/.test(value)) return math(value)
   flush()
   return result.length ? result : [literal]
  })
 }
 visit(tree)
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
