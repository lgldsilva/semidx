import ReactMarkdown from 'react-markdown'
import rehypeHighlight from 'rehype-highlight'
import remarkGfm from 'remark-gfm'
import '../styles/hljs.css'

// Element styling for rendered markdown, scoped with Tailwind arbitrary
// variants so no global CSS leaks out of chat answers.
const MD =
  'text-sm leading-relaxed ' +
  '[&>*:first-child]:mt-0 [&>*:last-child]:mb-0 ' +
  '[&_p]:my-2 ' +
  '[&_ul]:my-2 [&_ul]:list-disc [&_ul]:pl-5 ' +
  '[&_ol]:my-2 [&_ol]:list-decimal [&_ol]:pl-5 ' +
  '[&_h1]:mt-3 [&_h1]:mb-1.5 [&_h1]:text-base [&_h1]:font-bold ' +
  '[&_h2]:mt-3 [&_h2]:mb-1.5 [&_h2]:text-base [&_h2]:font-bold ' +
  '[&_h3]:mt-3 [&_h3]:mb-1 [&_h3]:text-sm [&_h3]:font-bold ' +
  '[&_h4]:mt-2 [&_h4]:mb-1 [&_h4]:text-sm [&_h4]:font-semibold ' +
  '[&_a]:text-accent [&_a]:underline ' +
  '[&_blockquote]:my-2 [&_blockquote]:border-l-4 [&_blockquote]:border-border [&_blockquote]:pl-3 [&_blockquote]:text-muted ' +
  '[&_hr]:my-3 [&_hr]:border-border ' +
  '[&_table]:my-2 [&_table]:w-full [&_table]:border-collapse ' +
  '[&_th]:border [&_th]:border-border [&_th]:px-2 [&_th]:py-1 [&_th]:text-left ' +
  '[&_td]:border [&_td]:border-border [&_td]:px-2 [&_td]:py-1 ' +
  '[&_pre]:my-2 [&_pre]:overflow-x-auto [&_pre]:rounded-md [&_pre]:bg-code-bg [&_pre]:p-3 [&_pre]:text-xs [&_pre]:text-code-fg ' +
  '[&_code]:rounded [&_code]:bg-code-bg [&_code]:px-1 [&_code]:py-0.5 [&_code]:font-mono [&_code]:text-[0.88em] [&_code]:text-code-fg ' +
  '[&_pre_code]:bg-transparent [&_pre_code]:p-0'

/**
 * MarkdownContent is the heavy half of components/Markdown.tsx: it pulls in
 * react-markdown + remark-gfm + rehype-highlight (and the hljs theme CSS), so
 * it must only be imported through React.lazy to stay in its own chunk.
 * rehype-raw is deliberately NOT used: raw HTML in model output stays inert.
 */
export default function MarkdownContent({ children }: Readonly<{ children: string }>) {
  return (
    <div className={MD}>
      <ReactMarkdown remarkPlugins={[remarkGfm]} rehypePlugins={[rehypeHighlight]}>
        {children}
      </ReactMarkdown>
    </div>
  )
}
