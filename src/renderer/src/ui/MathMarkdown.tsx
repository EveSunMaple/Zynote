import type {Components} from 'react-markdown'
import ReactMarkdown from 'react-markdown'
import rehypeKatex from 'rehype-katex'
import remarkCjkFriendly from 'remark-cjk-friendly/parseOnly'
import remarkCjkFriendlyGfmStrikethrough from 'remark-cjk-friendly-gfm-strikethrough/parseOnly'
import remarkGfm from 'remark-gfm'
import remarkMath from 'remark-math'
import {remarkChatMath} from '@shared/math-markdown'

const inlineComponents: Components = {
 p: ({children}) => <span>{children}</span>
}

/** Render Markdown with `$...$` / `$$...$$` math through KaTeX. */
export function MathMarkdown({
 children,
 inline = false,
 className,
 components
}: Readonly<{children: string; inline?: boolean; className?: string; components?: Components}>) {
 const Tag = inline ? 'span' : 'div'
 // CJK punctuation next to ** / ~~ needs the friendly extensions, which must follow remarkGfm.
 return (
  <Tag className={'math-markdown' + (className ? ' ' + className : '')}>
   <ReactMarkdown
    remarkPlugins={[remarkGfm, remarkCjkFriendly, remarkCjkFriendlyGfmStrikethrough, remarkChatMath, remarkMath]}
    rehypePlugins={[[rehypeKatex, {strict: 'ignore', output: 'html', trust: false}]]}
    components={inline ? {...inlineComponents, ...components} : components}
   >
    {children}
   </ReactMarkdown>
  </Tag>
 )
}
