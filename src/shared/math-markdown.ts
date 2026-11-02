import {unified, type Plugin} from 'unified'
import remarkParse from 'remark-parse'

const proseParser = unified().use(remarkParse).freeze()

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
  type Node = {
   type: string
   position?: {start: {offset?: number}; end: {offset?: number}}
   children?: Node[]
   alt?: string | null
  }
  const visit = (node: Node) => {
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
  const collectAlts = (node: Node) => {
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
  const restoreAlts = (node: Node) => {
   if (node.type === 'image' || node.type === 'imageReference') node.alt = imageAlts[imageIndex++]
   for (const child of node.children || []) restoreAlts(child)
  }
  restoreAlts(tree)
  return tree
 }
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
