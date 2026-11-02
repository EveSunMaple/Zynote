import {renderToStaticMarkup} from 'react-dom/server'
import {expect, it} from 'vitest'
import {MathMarkdown} from './MathMarkdown'
import {Markdown} from './UiParts'

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
