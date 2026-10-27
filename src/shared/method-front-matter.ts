export function methodFrontMatter(input: string) {
 const value = input.startsWith('\ufeff') ? input.slice(1) : input,
  opening = value.startsWith('---\r\n') ? 5 : value.startsWith('---\n') ? 4 : -1
 if (opening < 0) return undefined
 let cursor = opening
 for (;;) {
  const lineEnd = value.indexOf('\n', cursor),
   end = lineEnd < 0 ? value.length : lineEnd
  if (value.slice(cursor, end).replace(/\r$/, '') === '---') {
   if (lineEnd < 0 || lineEnd + 1 >= value.length || cursor === opening) return undefined
   return {header: value.slice(opening, cursor), body: value.slice(lineEnd + 1)}
  }
  if (lineEnd < 0) return undefined
  cursor = lineEnd + 1
 }
}
