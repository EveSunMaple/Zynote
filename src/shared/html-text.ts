import {decodeHTML} from 'entities'
import {Parser} from 'htmlparser2'

export function htmlToText(input: string): string {
 let text = '',
  skip = 0
 const parser = new Parser(
  {
   onopentag(name) {
    if (name === 'script' || name === 'style') skip++
    else if (!skip) text += ' '
   },
   onclosetag(name) {
    if ((name === 'script' || name === 'style') && skip > 0) skip--
    else if (!skip) text += ' '
   },
   ontext(value) {
    if (!skip) text += value
   }
  },
  {decodeEntities: false}
 )
 parser.write(input)
 parser.end()
 return decodeHTML(text).replace(/\s+/g, ' ').trim()
}
