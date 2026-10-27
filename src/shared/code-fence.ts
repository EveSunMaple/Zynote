export function stripCodeFence(raw: string, language = ''): string {
 let value = raw.trim()
 if (value.startsWith('```')) {
  value = value.slice(3)
  if (language && value.slice(0, language.length).toLowerCase() === language) value = value.slice(language.length)
  value = value.trimStart()
 }
 value = value.trimEnd()
 if (value.endsWith('```')) value = value.slice(0, -3)
 return value.trim()
}
