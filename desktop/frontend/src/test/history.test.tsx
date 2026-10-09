import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import "@/i18n"
import type { Conversation, Profile, SearchHit } from "@/lib/backend-types"
import { ConversationList } from "@/screens/assistant/conversation-list"
import { Highlighted } from "@/screens/assistant/conversation-search"
import { HistorySettings } from "@/screens/assistant/history-settings"
import { blankProfile } from "@/screens/assistant/profile-settings"

const b = vi.hoisted(() => ({
  search: vi.fn(),
  listPresets: vi.fn(),
  listProfiles: vi.fn(),
  getRetention: vi.fn(),
  setRetention: vi.fn(),
}))
vi.mock("@/lib/backend", () => ({ backend: b }))

const explore: Profile = { ...blankProfile(), id: "explore", name: "Explore", preset: true }

function conv(id: string, over: Partial<Conversation> = {}): Conversation {
  return {
    id,
    title: `Conversation ${id}`,
    site: "acme",
    mode: "read",
    providerID: "p",
    model: "m",
    profileID: "",
    created: "2026-10-01T10:00:00Z",
    updated: "2026-10-01T10:00:00Z",
    ...over,
  }
}

function hit(over: Partial<SearchHit> = {}): SearchHit {
  return {
    convID: "c1",
    title: "Conversation c1",
    site: "acme",
    msgID: "m1",
    snippet: "the overdue invoices of the quarter",
    updated: "2026-10-01T10:00:00Z",
    pinned: false,
    ...over,
  }
}

function list(over: Partial<React.ComponentProps<typeof ConversationList>> = {}) {
  const props = {
    conversations: [conv("c1"), conv("c2")],
    sites: ["acme", "beta"],
    selected: "c1",
    onSelect: vi.fn(),
    onNew: vi.fn(),
    onDelete: vi.fn().mockResolvedValue(undefined),
    onPin: vi.fn().mockResolvedValue(undefined),
    onArchive: vi.fn().mockResolvedValue(undefined),
    onExport: vi.fn().mockResolvedValue(undefined),
    onImport: vi.fn().mockResolvedValue(undefined),
    ...over,
  }
  render(<ConversationList {...props} />)
  return props
}

beforeEach(() => {
  b.listPresets.mockResolvedValue([explore])
  b.listProfiles.mockResolvedValue([])
  b.search.mockResolvedValue([hit()])
})
afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

async function type(text: string) {
  const box = screen.getByRole("searchbox", { name: "Search conversations" })
  fireEvent.change(box, { target: { value: text } })
  return box
}

describe("search box", () => {
  it("searches after typing, shows the matches with the words marked, and opens one", async () => {
    const p = list()
    await type("overdue invoices")
    await waitFor(() => expect(b.search).toHaveBeenCalled(), { timeout: 2000 })
    expect(b.search).toHaveBeenLastCalledWith(
      "overdue invoices",
      { site: "", profileID: "", from: "", to: "", archived: false },
      50,
    )
    const results = await screen.findByRole("list", { name: "Search results" })
    const marks = Array.from(results.querySelectorAll("mark")).map((m) => m.textContent)
    expect(marks).toEqual(["overdue", "invoices"])
    expect(screen.getByText("1 found")).toBeTruthy()
    // The conversation list gives way to the results.
    expect(screen.getByText("Conversation c2").closest("ul")?.className).toContain("hidden")
    fireEvent.click(within(results).getByRole("button", { name: /Conversation c1/ }))
    expect(p.onSelect).toHaveBeenCalledWith("c1")
  })

  it("does not search an empty box and clears the results with Escape", async () => {
    list()
    const box = await type("   ")
    await new Promise((r) => setTimeout(r, 350))
    expect(b.search).not.toHaveBeenCalled()
    fireEvent.change(box, { target: { value: "invoices" } })
    await screen.findByRole("list", { name: "Search results" }, { timeout: 2000 })
    fireEvent.keyDown(box, { key: "Escape" })
    await waitFor(() => expect(screen.queryByRole("list", { name: "Search results" })).toBeNull())
    expect((box as HTMLInputElement).value).toBe("")
  })

  it("sends the filters: site, profile and dates", async () => {
    list()
    fireEvent.click(screen.getByRole("button", { name: "Search filters" }))
    fireEvent.change(screen.getByLabelText("Site"), { target: { value: "beta" } })
    await waitFor(() => expect(screen.getByLabelText("Profile")).toBeTruthy())
    fireEvent.change(screen.getByLabelText("Profile"), { target: { value: "explore" } })
    fireEvent.change(screen.getByLabelText("From"), { target: { value: "2026-10-01" } })
    fireEvent.change(screen.getByLabelText("To"), { target: { value: "2026-10-09" } })
    await type("overdue")
    await waitFor(
      () =>
        expect(b.search).toHaveBeenLastCalledWith(
          "overdue",
          { site: "beta", profileID: "explore", from: "2026-10-01", to: "2026-10-09", archived: false },
          50,
        ),
      { timeout: 2000 },
    )
  })

  it("focuses the box on Ctrl+Shift+F", () => {
    list()
    const box = screen.getByRole("searchbox", { name: "Search conversations" })
    expect(document.activeElement).not.toBe(box)
    fireEvent.keyDown(window, { key: "F", ctrlKey: true, shiftKey: true })
    expect(document.activeElement).toBe(box)
  })

  it("searches the archive when the archive is shown", async () => {
    list({ conversations: [conv("c1"), conv("c2", { archived: true })] })
    fireEvent.click(screen.getByRole("button", { name: /Show the archive/ }))
    await type("overdue")
    await waitFor(() => expect(b.search).toHaveBeenLastCalledWith("overdue", expect.objectContaining({ archived: true }), 50), {
      timeout: 2000,
    })
  })

  it("shows why a search failed", async () => {
    b.search.mockRejectedValue(new Error("boom"))
    list()
    await type("overdue")
    await waitFor(() => expect(document.body.textContent).toContain("boom"), { timeout: 2000 })
  })

  it("marks only whole query words, escaping what a regex would read", () => {
    const { container } = render(<Highlighted text="cost (a+b) is 5" query="(a+b) [" />)
    expect(Array.from(container.querySelectorAll("mark")).map((m) => m.textContent)).toEqual(["(a+b)"])
  })
})

describe("pin and archive", () => {
  async function openMenu(title: string) {
    const trigger = screen.getByRole("button", { name: `More actions for ${title}` })
    fireEvent.pointerDown(trigger, { button: 0 })
    fireEvent.click(trigger)
    return await screen.findByRole("menu")
  }

  it("lists pinned conversations first and marks them", () => {
    list({ conversations: [conv("c1"), conv("c2", { pinned: true }), conv("c3")] })
    const titles = screen.getAllByRole("button", { name: /^(Pinned)?Conversation c\d/ }).map((x) => x.textContent)
    expect(titles[0]).toContain("Conversation c2")
    expect(screen.getByLabelText("Pinned")).toBeTruthy()
  })

  it("pins and unpins from the menu", async () => {
    const p = list({ conversations: [conv("c1"), conv("c2", { pinned: true })] })
    let menu = await openMenu("Conversation c1")
    fireEvent.click(within(menu).getByRole("menuitem", { name: "Pin to the top" }))
    await waitFor(() => expect(p.onPin).toHaveBeenCalledWith(expect.objectContaining({ id: "c1" }), true))
    cleanup()
    const q = list({ conversations: [conv("c1"), conv("c2", { pinned: true })] })
    menu = await openMenu("Conversation c2")
    fireEvent.click(within(menu).getByRole("menuitem", { name: "Unpin" }))
    await waitFor(() => expect(q.onPin).toHaveBeenCalledWith(expect.objectContaining({ id: "c2" }), false))
  })

  it("archives, keeps archived ones out of the list and brings them back from the archive view", async () => {
    const p = list({ conversations: [conv("c1"), conv("c2", { archived: true })] })
    expect(screen.queryByText("Conversation c2")).toBeNull()
    const menu = await openMenu("Conversation c1")
    fireEvent.click(within(menu).getByRole("menuitem", { name: "Archive" }))
    await waitFor(() => expect(p.onArchive).toHaveBeenCalledWith(expect.objectContaining({ id: "c1" }), true))

    fireEvent.click(screen.getByRole("button", { name: /Show the archive/ }))
    expect(screen.getByText("Conversation c2")).toBeTruthy()
    expect(screen.queryByText("Conversation c1")).toBeNull()
    const back = await openMenu("Conversation c2")
    fireEvent.click(within(back).getByRole("menuitem", { name: "Move out of the archive" }))
    await waitFor(() => expect(p.onArchive).toHaveBeenCalledWith(expect.objectContaining({ id: "c2" }), false))
    fireEvent.click(screen.getByRole("button", { name: "Back to conversations" }))
    expect(screen.getByText("Conversation c1")).toBeTruthy()
  })

  it("exports as JSON or Markdown and imports", async () => {
    const p = list()
    let menu = await openMenu("Conversation c1")
    fireEvent.click(within(menu).getByRole("menuitem", { name: "Export as Markdown" }))
    await waitFor(() => expect(p.onExport).toHaveBeenCalledWith(expect.objectContaining({ id: "c1" }), "md"))
    cleanup()
    const q = list()
    menu = await openMenu("Conversation c2")
    fireEvent.click(within(menu).getByRole("menuitem", { name: "Export as JSON" }))
    await waitFor(() => expect(q.onExport).toHaveBeenCalledWith(expect.objectContaining({ id: "c2" }), "json"))
    fireEvent.click(screen.getByRole("button", { name: "Import a conversation" }))
    expect(q.onImport).toHaveBeenCalled()
  })
})

describe("history settings", () => {
  beforeEach(() => {
    b.getRetention.mockResolvedValue(0)
    b.setRetention.mockResolvedValue(undefined)
  })

  it("shows the period and asks before keeping less", async () => {
    render(<HistorySettings />)
    const select = (await screen.findByLabelText("Keep conversations")) as HTMLSelectElement
    expect(select.value).toBe("0")
    fireEvent.change(select, { target: { value: "30" } })
    expect(await screen.findByText("Delete conversations after 30 days?")).toBeTruthy()
    expect(b.setRetention).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole("button", { name: "Keep 30 days" }))
    await waitFor(() => expect(b.setRetention).toHaveBeenCalledWith(30))
    await waitFor(() => expect((screen.getByLabelText("Keep conversations") as HTMLSelectElement).value).toBe("30"))
  })

  it("changes nothing when the question is cancelled", async () => {
    render(<HistorySettings />)
    const select = await screen.findByLabelText("Keep conversations")
    fireEvent.change(select, { target: { value: "90" } })
    await screen.findByText("Delete conversations after 90 days?")
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }))
    await waitFor(() => expect(screen.queryByText("Delete conversations after 90 days?")).toBeNull())
    expect(b.setRetention).not.toHaveBeenCalled()
    expect((screen.getByLabelText("Keep conversations") as HTMLSelectElement).value).toBe("0")
  })

  it("goes back to forever without asking", async () => {
    b.getRetention.mockResolvedValue(90)
    render(<HistorySettings />)
    const select = await screen.findByLabelText("Keep conversations")
    await waitFor(() => expect((select as HTMLSelectElement).value).toBe("90"))
    fireEvent.change(select, { target: { value: "0" } })
    await waitFor(() => expect(b.setRetention).toHaveBeenCalledWith(0))
  })
})
