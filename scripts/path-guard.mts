import {isAbsolute, relative, resolve} from 'node:path'

export function containsPath(root: string, target: string): boolean {
 const part = relative(resolve(root), resolve(target))
 return (
  part === '' ||
  (part !== '..' && !part.startsWith('..' + (process.platform === 'win32' ? '\\' : '/')) && !isAbsolute(part))
 )
}

export function requireInside(root: string, target: string, label: string): string {
 const resolved = resolve(target)
 if (!containsPath(root, resolved)) throw new Error(label + ' must stay inside ' + resolve(root))
 return resolved
}
