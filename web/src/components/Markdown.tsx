import { lazy, Suspense } from 'react'

// Code-split: react-markdown + highlight only load on the first chat answer.
const MarkdownContent = lazy(() => import('./MarkdownContent'))

/** Renders GFM markdown with syntax-highlighted code blocks (lazy chunk);
 * falls back to plain preformatted text while the chunk loads. */
export function Markdown({ children }: Readonly<{ children: string }>) {
  return (
    <Suspense
      fallback={<pre className="m-0 font-sans text-sm whitespace-pre-wrap">{children}</pre>}
    >
      <MarkdownContent>{children}</MarkdownContent>
    </Suspense>
  )
}
