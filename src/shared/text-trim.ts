export function stripTrailing(value: string, chars: string): string {
 let end = value.length
 while (end > 0 && chars.includes(value[end - 1])) end--
 return value.slice(0, end)
}

export function stripLeading(value: string, chars: string): string {
 let start = 0
 while (start < value.length && chars.includes(value[start])) start++
 return value.slice(start)
}
