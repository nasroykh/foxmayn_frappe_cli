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

  it("handles reference links and drops reference images", () => {
    const { container } = render(
      <ChatMarkdown text={"see [the docs][d] and ![pic][p]\n\n[d]: https://example.com/ref\n[p]: https://example.com/p.png"} />,
    )
    expect(screen.getByRole("link", { name: "the docs" }).getAttribute("href")).toBe("https://example.com/ref")
    expect(container.querySelector("img")).toBeNull()
    expect(container.textContent).toContain("pic")
  })

  it("makes a link of a www autolink and opens it", () => {
    render(<ChatMarkdown text="visit www.example.com now" />)
    fireEvent.click(screen.getByRole("link", { name: "www.example.com" }))
    expect(openWebsite).toHaveBeenCalledWith("http://www.example.com")
  })

  it("renders footnotes without making their jump links open anything", () => {
    const { container } = render(<ChatMarkdown text={"text[^1]\n\n[^1]: the note"} />)
    expect(container.textContent).toContain("the note")
    fireEvent.click(container.querySelector("a") ?? container)
    expect(openWebsite).not.toHaveBeenCalled()
  })

  it("renders task lists as disabled checkboxes", () => {
    const { container } = render(<ChatMarkdown text={"- [x] done\n- [ ] todo"} />)
    const boxes = container.querySelectorAll('input[type="checkbox"]')
    expect(boxes).toHaveLength(2)
    expect([...boxes].every((b) => (b as HTMLInputElement).disabled)).toBe(true)
  })

  it("makes no link of data: or vbscript: addresses", () => {
    const { container } = render(
      <ChatMarkdown text="[a](data:text/html;base64,PHNjcmlwdD4=) [b](vbscript:msgbox(1)) [c](  javascript:alert(1))" />,
    )
    expect(container.querySelector("a")).toBeNull()
  })

  it("opens on a middle click only, and never on another button", () => {
    render(<ChatMarkdown text="[docs](https://example.com/docs)" />)
    const link = screen.getByRole("link", { name: "docs" })
    expect(fireEvent(link, new MouseEvent("auxclick", { bubbles: true, cancelable: true, button: 2 }))).toBe(false)
    expect(openWebsite).not.toHaveBeenCalled()
    expect(fireEvent(link, new MouseEvent("auxclick", { bubbles: true, cancelable: true, button: 1 }))).toBe(false)
    expect(openWebsite).toHaveBeenCalledWith("https://example.com/docs")
    expect(link.getAttribute("draggable")).toBe("false")
  })

  it("shows the real host when the link text does not name it", () => {
    const { container } = render(
      <ChatMarkdown text="[your bank](https://evil.example.net/login) and [www.good.com](https://www.good.com/x)" />,
    )
    expect(container.textContent).toContain("your bank (evil.example.net)")
    expect(container.textContent).not.toContain("(www.good.com)")
  })
})
