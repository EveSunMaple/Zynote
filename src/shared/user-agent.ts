export function cleanUserAgent(value: string) {
 return value
  .split(/\s+/)
  .filter(token => !/^(?:Electron|listening-assistant)\//.test(token))
  .join(' ')
}
