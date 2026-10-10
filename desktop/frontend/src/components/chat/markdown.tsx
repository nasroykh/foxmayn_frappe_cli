// Assistant text. The model's output is untrusted: raw HTML is skipped (never
// rendered, never parsed with rehype-raw), images are dropped to their alt
// text (no request leaves the app), and links open in the system browser
// through the backend, never in this web view.
import { isValidElement } from "react"
import type * as React from "react"
import Markdown, { type Components } from "react-markdown"
import remarkGfm from "remark-gfm"
import { cn } from "cn"

import { backend } from "@/lib/backend"

const remarkPlugins = [remarkGfm]

function textOf(node: React.ReactNode): string {
  if (node == null || typeof node === "boolean") return ""
  if (typeof node === "string" || typeof node === "number") return String(node)
  if (Array.isArray(node)) return node.map(textOf).join("")
  if (isValidElement<{ children?: React.ReactNode }>(node)) return textOf(node.props.children)
  return ""
}

function hostOf(href: string): string {
  try {
    return new URL(href).host
  } catch {
    return ""
  }
}

function Link({ href, children }: { href?: string; children?: React.ReactNode }) {
  // Only web addresses open; anything else (mailto:, relative, odd schemes) stays text.
  if (!href || !/^https?:\/\//i.test(href)) return <span>{children}</span>
  const host = hostOf(href)
  // The text of a link must not fake where it goes: show the real host when the text does not.
  const text = textOf(children).toLowerCase()
  const showHost = !!host && !text.includes(host.toLowerCase())
  const open = (e: React.SyntheticEvent) => {
    e.preventDefault()
    e.stopPropagation()
    void backend.openWebsite(href).catch(() => {})
  }
  return (
    <>
      <a
        href={href}
        title={href}
        rel="noreferrer noopener"
        draggable={false}
        onClick={open}
        onAuxClick={(e) => {
          e.preventDefault()
          e.stopPropagation()
          if (e.button === 1) void backend.openWebsite(href).catch(() => {})
        }}
        className="text-primary underline underline-offset-2"
      >
        {children}
      </a>
      {showHost && <span className="text-muted-foreground text-xs"> ({host})</span>}
    </>
  )
}

const components: Components = {
  a: ({ href, children }) => <Link href={href}>{children}</Link>,
  img: ({ alt }) => (alt ? <span>{alt}</span> : null),
  p: ({ children }) => <p dir="auto" className="leading-relaxed [&:not(:first-child)]:mt-2">{children}</p>,
  h1: ({ children }) => <h3 dir="auto" className="font-heading mt-3 text-base font-semibold">{children}</h3>,
  h2: ({ children }) => <h3 dir="auto" className="font-heading mt-3 text-base font-semibold">{children}</h3>,
  h3: ({ children }) => <h4 dir="auto" className="mt-3 text-sm font-semibold">{children}</h4>,
  h4: ({ children }) => <h4 dir="auto" className="mt-3 text-sm font-semibold">{children}</h4>,
  h5: ({ children }) => <h5 dir="auto" className="mt-3 text-sm font-medium">{children}</h5>,
  h6: ({ children }) => <h5 dir="auto" className="mt-3 text-sm font-medium">{children}</h5>,
  ul: ({ children }) => <ul dir="auto" className="mt-2 list-disc ps-5">{children}</ul>,
  ol: ({ children }) => <ol dir="auto" className="mt-2 list-decimal ps-5">{children}</ol>,
  blockquote: ({ children }) => (
    <blockquote dir="auto" className="text-muted-foreground mt-2 border-s-2 ps-3">{children}</blockquote>
  ),
  hr: () => <hr className="my-3" />,
  table: ({ children }) => (
    <div className="mt-2 max-w-full overflow-x-auto">
      <table className="w-full border-collapse text-sm">{children}</table>
    </div>
  ),
  th: ({ children }) => <th dir="auto" className="border px-2 py-1 text-start font-medium">{children}</th>,
  td: ({ children }) => <td dir="auto" className="border px-2 py-1">{children}</td>,
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

// Each block takes the direction of its own text (dir="auto"): a reply can mix
// Arabic and English paragraphs, and an Arabic one often starts with a Latin
// document name.
export function ChatMarkdown({ text }: { text: string }) {
  return (
    <div className="min-w-0 text-sm break-words">
      <Markdown remarkPlugins={remarkPlugins} skipHtml components={components}>
        {text}
      </Markdown>
    </div>
  )
}
