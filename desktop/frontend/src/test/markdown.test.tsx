import { cleanup, fireEvent, render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"

import { ChatMarkdown } from "@/components/chat/markdown"

const openWebsite = vi.hoisted(() => vi.fn(() => Promise.resolve()))
vi.mock("@/lib/backend", () => ({ backend: { openWebsite } }))

afterEach(() => {
  cleanup()
  openWebsite.mockClear()
})

describe("ChatMarkdown", () => {
  it("renders markdown and gfm", () => {
    const { container } = render(<ChatMarkdown text={"**bold**\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n```\ncode\n```"} />)
    expect(container.querySelector("strong")?.textContent).toBe("bold")
    expect(container.querySelector("table")).not.toBeNull()
    expect(container.querySelector("pre code")?.textContent).toContain("code")
  })

  it("drops script and raw html", () => {
    const text = '<script>alert(1)</script>\n\nhello <b>raw</b> <iframe src="https://x"></iframe>\n\n<img src=x onerror=alert(2)>'
    const { container } = render(<ChatMarkdown text={text} />)
    expect(container.querySelector("script")).toBeNull()
    expect(container.querySelector("b")).toBeNull()
    expect(container.querySelector("iframe")).toBeNull()
    expect(container.querySelector("img")).toBeNull()
    expect(container.innerHTML).not.toContain("onerror")
    expect(container.textContent).toContain("hello")
  })

  it("drops images and keeps the alt text", () => {
    const { container } = render(<ChatMarkdown text="![a chart](https://example.com/c.png)" />)
    expect(container.querySelector("img")).toBeNull()
    expect(container.textContent).toContain("a chart")
  })

  it("opens links through openWebsite and never navigates", () => {
    render(<ChatMarkdown text="[docs](https://example.com/docs)" />)
    const link = screen.getByRole("link", { name: "docs" })
    const notPrevented = fireEvent.click(link)
    expect(notPrevented).toBe(false)
    expect(openWebsite).toHaveBeenCalledWith("https://example.com/docs")
  })

  it("does not make a link of other schemes", () => {
    const { container } = render(<ChatMarkdown text="[x](javascript:alert(1)) [m](mailto:a@b.c) [r](/relative)" />)
    expect(container.querySelector("a")).toBeNull()
    expect(container.textContent).toContain("x")
  })
})
