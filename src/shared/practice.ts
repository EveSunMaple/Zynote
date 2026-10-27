const compareCodeUnit = (a: string, b: string) => Number(a > b) - Number(a < b)
const optionPattern = /^\s*([A-H])[.、．)）:：]\s*/
const choiceSuffix = '.。、，,:：'
const choiceSeparator = '、,，'
const isSpace = (value: string) => /\s/.test(value)

export function questionOptions(stem: string) {
 const options: {key: string; text: string}[] = []
 for (const line of stem.split('\n')) {
  const match = optionPattern.exec(line)
  if (match) options.push({key: match[1], text: line.slice(match[0].length)})
  else if (options.length) options[options.length - 1].text += '\n' + line // NOSONAR: sorting semantics depend on the current array copy behavior.
 }
 if (options.length < 2 || new Set(options.map(option => option.key)).size !== options.length) return []
 return options.map(option => ({key: option.key, text: option.text.trim()}))
}
export function questionStem(stem: string) {
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
