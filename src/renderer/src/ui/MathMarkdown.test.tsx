import {renderToStaticMarkup} from 'react-dom/server'
import {expect, it} from 'vitest'
import {createRequire} from 'node:module'
import {readFileSync} from 'node:fs'
import {dirname, resolve} from 'node:path'
import {MathMarkdown} from './MathMarkdown'
import {Markdown} from './UiParts'

it('uses the same KaTeX version for imported styles and rehype rendering', () => {
 const require = createRequire(import.meta.url)
 const stylesVersion = require('katex/package.json').version
 const renderer = require.resolve('katex', {paths: [require.resolve('rehype-katex')]})
 const rendererVersion = JSON.parse(readFileSync(resolve(dirname(renderer), '../package.json'), 'utf8')).version
 expect(stylesVersion).toBe(rendererVersion)
})

it('renders inline and display math through KaTeX', () => {
 const markup = renderToStaticMarkup(<MathMarkdown>{'行内 $x^2$\n\n$$\n\\frac{a}{b}\n$$\n'}</MathMarkdown>)
 expect(markup).toContain('class="katex"')
 expect(markup).toContain('katex-display')
 expect(markup).toContain('行内')
})

it('renders inline content without block paragraphs', () => {
 const markup = renderToStaticMarkup(<MathMarkdown inline>{'极限 $\\sqrt[n]{a}$'}</MathMarkdown>)
 expect(markup).toContain('<span class="math-markdown">')
 expect(markup).not.toContain('<p>')
 expect(markup).toContain('katex')
})

it('keeps the chat Markdown wrapper math-aware', () => {
 const markup = renderToStaticMarkup(<Markdown text={'极限 $x^2$'} />)
 expect(markup).toContain('class="katex"')
 expect(markup).not.toContain('$x^2$')
})

it('renders CJK punctuation emphasis in inline mode', () => {
 const markup = renderToStaticMarkup(<MathMarkdown inline>{'搭好**“牙体vs牙髓”的章节地图**、理清'}</MathMarkdown>)
 expect(markup).toContain('<strong>“牙体vs牙髓”的章节地图</strong>')
 expect(markup).not.toContain('**')
})
