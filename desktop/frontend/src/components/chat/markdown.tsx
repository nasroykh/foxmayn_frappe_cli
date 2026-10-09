// Assistant text. The model's output is untrusted: raw HTML is skipped (never
// rendered, never parsed with rehype-raw), images are dropped to their alt
// text (no request leaves the app), and links open in the system browser
// through the backend, never in this web view.
import type * as React from "react"
import Markdown, { type Components } from "react-markdown"
import remarkGfm from "remark-gfm"
import { cn } from "cn"

import { backend } from "@/lib/backend"

const remarkPlugins = [remarkGfm]

function Link({ href, children }: { href?: string; children?: React.ReactNode }) {
  // Only web addresses open; anything else (mailto:, relative, odd schemes) stays text.
  if (!href || !/^https?:\/\//i.test(href)) return <span>{children}</span>
  const open = (e: React.SyntheticEvent) => {
    e.preventDefault()
    e.stopPropagation()
    void backend.openWebsite(href).catch(() => {})
  }
  return (
    <a
      href={href}
      title={href}
      rel="noreferrer noopener"
      onClick={open}
      onAuxClick={open}
      className="text-primary underline underline-offset-2"
    >
      {children}
    </a>
  )
}

const components: Components = {
  a: ({ href, children }) => <Link href={href}>{children}</Link>,
  img: ({ alt }) => (alt ? <span>{alt}</span> : null),
  p: ({ children }) => <p className="leading-relaxed [&:not(:first-child)]:mt-2">{children}</p>,
  h1: ({ children }) => <h3 className="font-heading mt-3 text-base font-semibold">{children}</h3>,
  h2: ({ children }) => <h3 className="font-heading mt-3 text-base font-semibold">{children}</h3>,
  h3: ({ children }) => <h4 className="mt-3 text-sm font-semibold">{children}</h4>,
  h4: ({ children }) => <h4 className="mt-3 text-sm font-semibold">{children}</h4>,
  h5: ({ children }) => <h5 className="mt-3 text-sm font-medium">{children}</h5>,
  h6: ({ children }) => <h5 className="mt-3 text-sm font-medium">{children}</h5>,
  ul: ({ children }) => <ul className="mt-2 list-disc pl-5">{children}</ul>,
  ol: ({ children }) => <ol className="mt-2 list-decimal pl-5">{children}</ol>,
  blockquote: ({ children }) => (
    <blockquote className="text-muted-foreground mt-2 border-l-2 pl-3">{children}</blockquote>
  ),
  hr: () => <hr className="my-3" />,
  table: ({ children }) => (
    <div className="mt-2 max-w-full overflow-x-auto">
      <table className="w-full border-collapse text-sm">{children}</table>
    </div>
  ),
  th: ({ children }) => <th className="border px-2 py-1 text-left font-medium">{children}</th>,
  td: ({ children }) => <td className="border px-2 py-1">{children}</td>,
  pre: ({ children }) => (
    <pre className="bg-muted mt-2 max-h-72 overflow-auto rounded-lg p-3 font-mono text-xs leading-relaxed">
      {children}
    </pre>
  ),
  // A fenced block has a language class or a newline; inline code gets a chip.
  code: ({ className, children }) => (
    <code
      className={cn(
        "font-mono",
        !(className || String(children).includes("\n")) && "bg-muted rounded px-1 py-0.5 text-[0.85em]",
      )}
    >
      {children}
    </code>
  ),
}

export function ChatMarkdown({ text }: { text: string }) {
  return (
    <div className="min-w-0 text-sm break-words">
      <Markdown remarkPlugins={remarkPlugins} skipHtml components={components}>
        {text}
      </Markdown>
    </div>
  )
}
