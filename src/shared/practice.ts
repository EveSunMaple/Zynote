type Option = {key: string; text: string}
type InlineOption = {key: string; prefixStart: number; end: number}
const compareCodeUnit = (a: string, b: string) => Number(a > b) - Number(a < b)
const optionPattern = /^\s*([A-H])[.、．)）:：]\s*/
const inlineOptionPattern = /(^|\s)([A-H])[.、．)）:：]\s*/g
const choiceSuffix = '.。、，,:：'
const choiceSeparator = '、,，'
const isSpace = (value: string) => /\s/.test(value)

function lineOptions(stem: string): Option[] {
 const options: Option[] = []
 for (const line of stem.split('\n')) {
  const match = optionPattern.exec(line)
  if (match) options.push({key: match[1], text: line.slice(match[0].length)})
  else if (options.length) options[options.length - 1].text += '\n' + line // NOSONAR: sorting semantics depend on the current array copy behavior.
 }
 if (options.length < 2 || new Set(options.map(option => option.key)).size !== options.length) return []
 return options.map(option => ({key: option.key, text: option.text.trim()}))
}

function inlineOptions(stem: string): {options: Option[]; start: number} {
 const matches: InlineOption[] = []
 inlineOptionPattern.lastIndex = 0
 for (const match of stem.matchAll(inlineOptionPattern)) {
  const prefixStart = match.index ?? 0
  matches.push({key: match[2], prefixStart, end: prefixStart + match[0].length})
 }
 let best: InlineOption[] = []
 for (const candidate of matches) {
  if (candidate.key !== 'A') continue
  const sequence: InlineOption[] = []
  let expected = 'A'.charCodeAt(0)
  for (const match of matches.slice(matches.indexOf(candidate))) {
   if (match.key.charCodeAt(0) !== expected) break
   sequence.push(match)
   expected++
  }
  if (sequence.length > best.length) best = sequence
 }
 if (best.length < 2) return {options: [], start: -1}
 const options: Option[] = []
 for (let index = 0; index < best.length; index++) {
  const text = stem.slice(best[index].end, best[index + 1]?.prefixStart ?? stem.length).trim()
  if (!text) return {options: [], start: -1}
  options.push({key: best[index].key, text})
 }
 return {options, start: best[0].prefixStart}
}

export function questionOptions(stem: string) {
 const line = lineOptions(stem),
  inline = inlineOptions(stem).options
 return inline.length > line.length ? inline : line
}
export function questionStem(stem: string) {
 const line = lineOptions(stem),
  inline = inlineOptions(stem)
 if (inline.options.length > line.length && inline.start >= 0) return stem.slice(0, inline.start).trim()
 const lines = stem.split('\n'),
  index = lines.findIndex((line, position) => position > 0 && /^\s*A[.、．)）:：]/.test(line))
 return index < 0 ? stem : lines.slice(0, index).join('\n')
}
export function answerChoices(answer: string, allowed: string[]) {
 let value = answer.trim()
 for (const prefix of ['参考答案', '正确答案', '答案'])
  if (value.startsWith(prefix)) {
   value = value.slice(prefix.length).trimStart()
   break
  }
 if (value.startsWith(':') || value.startsWith('：')) value = value.slice(1).trimStart()
 if (!/^[A-H]/.test(value)) return []
 const keys: string[] = []
 let valid: string[] | undefined
 for (let index = 0; index < value.length; index++) {
  const char = value[index]
  if (/[A-H]/.test(char)) {
   keys.push(char)
   const next = value[index + 1]
   if (next === undefined || choiceSuffix.includes(next) || isSpace(next)) valid = [...keys]
   continue
  }
  if (choiceSeparator.includes(char) || isSpace(char)) continue
  break
 }
 if (!valid?.every(key => allowed.includes(key))) return []
 return [...new Set(valid)].sort(compareCodeUnit)
}
export function compareChoices(response: string, correct: string[]) {
 if (!/^[A-H](?:[,，、\s]*[A-H])*$/.test(response.trim())) return false
 return (
  [...new Set(response.match(/[A-H]/g) || [])].sort(compareCodeUnit).join('') ===
  [...new Set(correct)].sort(compareCodeUnit).join('')
 )
}
