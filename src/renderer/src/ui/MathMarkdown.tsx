import type {Components} from 'react-markdown'
import ReactMarkdown from 'react-markdown'
import rehypeKatex from 'rehype-katex'
import remarkGfm from 'remark-gfm'
import remarkMath from 'remark-math'

const inlineComponents: Components = {
 p: ({children}) => <span>{children}</span>
}

/** Render Markdown with `$...$` / `$$...$$` math through KaTeX. */
export function MathMarkdown({
 children,
 inline = false,
 className
}: Readonly<{children: string; inline?: boolean; className?: string}>) {
 const Tag = inline ? 'span' : 'div'
 return (
  <Tag className={'math-markdown' + (className ? ' ' + className : '')}>
   <ReactMarkdown
    remarkPlugins={[remarkGfm, remarkMath]}
    rehypePlugins={[[rehypeKatex, {strict: 'ignore', output: 'html'}]]}
    components={inline ? inlineComponents : undefined}
   >
    {children}
   </ReactMarkdown>
  </Tag>
 )
}
