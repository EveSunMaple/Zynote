import {renderToStaticMarkup} from 'react-dom/server'
import {describe, expect, it} from 'vitest'
import {Markdown} from './UiParts'

const render = (text: string) => renderToStaticMarkup(<Markdown text={text} />)
const mathCount = (html: string) => (html.match(/class="katex"/g) || []).length

describe('chat Markdown actual rendering pipeline', () => {
 it('renders inline, display and model-style backslash math', () => {
  for (const text of ['$x^2$', '$$\nx^2\n$$', String.raw`\(x^2\)`, String.raw`\[x^2\]`]) {
   expect(mathCount(render(text))).toBe(1)
  }
 })
 it('keeps Unicode/CRLF offsets, linked images and reference image alt text intact', () => {
  const html = render(
   '中文😀\r\n\r\n> ```js\r\n> \\(x\\)\r\n> ```\r\n\r\n[![alt $x$](https://example.org/img)](https://example.org/a$x$)\r\n\r\n![ref $y$][img]\r\n\r\n[img]: https://example.org/ref "title $z$"\r\n\r\n$z^2$'
  )
  expect(mathCount(html)).toBe(1)
  expect(html).toContain('alt="alt $x$"')
  expect(html).toContain('alt="ref $y$"')
  expect(html).toContain('title="title $z$"')
  expect(html).toContain('href="https://example.org/a$x$"')
 })
 it('leaves code, escaped dollars, currency and incomplete streams literal', () => {
  for (const text of [
   '`$x$`',
   '```js\n$x$\n```',
   '    $x$',
   String.raw`\$x\$`,
   '$5 and $10',
   '$x + ',
   String.raw`\(x +`
  ]) {
   expect(mathCount(render(text)), text).toBe(0)
  }
  expect(render('$5 and $10')).toContain('$5 and $10')
  expect(mathCount(render('$x + y$'))).toBe(1)
  expect(mathCount(render(String.raw`\(x + y\)`))).toBe(1)
 })
 it('retains GFM and safe link/HTML behavior and never trusts KaTeX HTML commands', () => {
  const html = render(
   '[safe](https://example.org) [bad](javascript:alert(1))\n\n<script>alert(1)</script>\n\n|a|b|\n|-|-|\n|1|2|\n\n$\\href{javascript:alert(1)}{x}$'
  )
  expect(html).toContain('href="https://example.org"')
  expect(html).not.toContain('href="javascript:')
  expect(html).not.toContain('<script>')
  expect(html).toContain('<table>')
 })
 it('does not turn literal escaped backslashes or mixed formula/text lines into math', () => {
  expect(mathCount(render(String.raw`\\(x\\)`))).toBe(0)
  expect(mathCount(render('~~~js\n\\[x\\]\n~~~'))).toBe(0)
  for (const text of ['> ```tex\n> \\(x\\) $5 and $10\n> ```', '- item\n\n  ```tex\n  \\[x\\] $x$\n  ```']) {
   expect(mathCount(render(text))).toBe(0)
  }
  expect(render('[target](https://example.org/a$x$)')).toContain('href="https://example.org/a$x$"')
  expect(render('![alt $x$](https://example.org/a.png "title $x$")')).toContain('alt="alt $x$"')
  expect(render(String.raw`\\$5 and $10`)).toContain('\\$5 and $10')
 })
 it('renders closed math/LaTeX fences including bare LaTeX and both fence markers', () => {
  for (const language of ['math', 'latex', 'tex', 'katex', 'TEX']) {
   for (const formula of [
    String.raw`\frac{1}{2}`,
    String.raw`\text{hello}`,
    String.raw`\operatorname{sin}(x)`,
    'x^2',
    '$1$',
    '$x^2$',
    String.raw`\(x^2\)`
   ]) {
    expect(mathCount(render(`\`\`\`${language}\n${formula}\n\`\`\``)), `${language}: ${formula}`).toBe(1)
   }
  }
  expect(mathCount(render('~~~latex\nx^2\n~~~'))).toBe(1)
  expect(mathCount(render('> ```tex\n> \\(x^2\\)\n> ```'))).toBe(1)
  expect(mathCount(render('- item\n\n  ```latex\n  \\frac{1}{2}\n  ```'))).toBe(1)
  for (const formula of [
   String.raw`\begin{aligned}x &= 1 \\ y &= 2\;\end{aligned}`,
   String.raw`\begin{pmatrix}1 & 2 \\ 3 & 4\end{pmatrix}`
  ]) {
   const html = render(`\`\`\`latex\n${formula}\n\`\`\``)
   expect(mathCount(html)).toBe(1)
   expect(html).not.toContain('katex-error')
  }
 })
 it('renders the reported mixed fence formula lines and retains the long non-formula code', () => {
  for (const language of ['tex', 'latex', 'math', '']) {
   const html = render(`\`\`\`${language}\n$x^2$\n\\(x^2\\)\nlong_source_long_source\n\`\`\``)
   expect(mathCount(html)).toBe(2)
   expect(html).toMatch(/<pre><code[^>]*>long_source_long_source\n<\/code><\/pre>/)
  }
  expect(mathCount(render('```tex\n$$\nx^2 + y^2\n$$\n\\[\nz^2\n\\]\n```'))).toBe(2)
 })
 it('keeps programs, currency, inline code and unfinished streamed fences literal', () => {
  for (const text of [
   '```js\n$x$\n```',
   '```python\n\\(x\\)\n```',
   '```\nconst x = 2;\n$x$\n```',
   '```tex\n$5 and $10\n```',
   '```\nlong_source\n```',
   '`$x^2$`',
   '```tex\nx^2',
   '```math\nx^2',
   '```latex\n\\(x^2',
   '```tex\n$$\nx^2\n```'
  ])
   expect(mathCount(render(text)), text).toBe(0)
  expect(mathCount(render('```tex\nx^2\n```'))).toBe(1)
  expect(mathCount(render('```tex\n' + '\\[\n'.repeat(2000) + '```'))).toBe(0)
  expect(mathCount(render('```tex\n' + '$$\n'.repeat(2000) + '```'))).toBe(0)
 })
 it('shows invalid LaTeX visibly and never trusts commands from a formula fence', () => {
  expect(render('```latex\n\\frac{\n```')).toContain('katex-error')
  expect(render('```latex\n\\notARealCommand{x}\n```')).toContain('notARealCommand')
  const html = render('```latex\n\\href{javascript:alert(1)}{x}\n```')
  expect(html).not.toContain('href="javascript:')
  expect(html).not.toContain('<script>')
 })
})
