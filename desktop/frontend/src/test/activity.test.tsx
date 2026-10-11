import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import "@/i18n"
import { TooltipProvider } from "@/components/ui/tooltip"
import type { ActivityEntry, ActivityPage } from "@/lib/backend-types"
import { ActivityScreen } from "@/screens/activity-screen"

const b = vi.hoisted(() => ({ queryActivity: vi.fn() }))
vi.mock("@/lib/backend", () => ({ backend: b }))

function entry(i: number, over: Partial<ActivityEntry> = {}): ActivityEntry {
  return {
    time: new Date(Date.now() - i * 3_600_000).toISOString(),
    site: "acme-prod",
    client: "foxmayn-desktop",
    tool: "get_doc",
    runID: "",
    via: "",
    doctypes: ["ToDo"],
    names: [`TD-${String(i).padStart(4, "0")}`],
    namesTotal: 1,
    method: "",
    args: "",
    status: "ok",
    confirm: "",
    error: "",
    durationMS: 120,
    ...over,
  }
}

function pageOf(entries: ActivityEntry[], over: Partial<ActivityPage> = {}): ActivityPage {
  return {
    entries,
    total: entries.length,
    skipped: 0,
    sites: ["acme-prod"],
    clients: ["claude-ai", "foxmayn-desktop"],
    tools: ["get_doc"],
    statuses: ["denied", "ok"],
    exists: true,
    ...over,
  }
}

const lastQuery = () => b.queryActivity.mock.calls[b.queryActivity.mock.calls.length - 1][0]

function open() {
  return render(
    <TooltipProvider>
      <ActivityScreen />
    </TooltipProvider>,
  )
}

beforeEach(() => {
  b.queryActivity.mockResolvedValue(pageOf([entry(1), entry(2)]))
})
afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

describe("activity screen", () => {
  it("lists the entries in the order the log gives them (newest first)", async () => {
    b.queryActivity.mockResolvedValue(pageOf([entry(1, { tool: "get_doc" }), entry(5, { tool: "list_docs" }), entry(9, { tool: "create_doc" })]))
    open()
    const list = await screen.findByRole("list", { name: "Tool calls" })
    const rows = within(list).getAllByRole("listitem")
    expect(rows.map((r) => r.textContent)).toEqual([
      expect.stringContaining("Read a document"),
      expect.stringContaining("Listed documents"),
      expect.stringContaining("Created a document"),
    ])
    expect(within(rows[0]).getByText("TD-0001", { exact: false })).toBeTruthy()
    expect(within(rows[0]).getByText("This app's Assistant")).toBeTruthy()
    expect(screen.getByText("Showing 3 of 3")).toBeTruthy()
    expect(b.queryActivity.mock.calls[0][0]).toMatchObject({ client: "", offset: 0, limit: 50 })
  })

  it("asks again with the app when it is filtered", async () => {
    open()
    await screen.findByRole("list", { name: "Tool calls" })
    fireEvent.change(screen.getByLabelText("App"), { target: { value: "claude-ai" } })
    await waitFor(() => expect(lastQuery()).toMatchObject({ client: "claude-ai", offset: 0 }))
    expect(screen.getByRole("button", { name: "Clear filters" })).toBeTruthy()
  })

  it("loads the next page with the offset and appends it", async () => {
    const first = Array.from({ length: 50 }, (_, i) => entry(i + 1))
    b.queryActivity.mockResolvedValueOnce(pageOf(first, { total: 52 }))
    b.queryActivity.mockResolvedValueOnce(pageOf([entry(60), entry(61)], { total: 52 }))
    open()
    expect(await screen.findByText("Showing 50 of 52")).toBeTruthy()
    fireEvent.click(screen.getByRole("button", { name: "Load more" }))
    expect(await screen.findByText("Showing 52 of 52")).toBeTruthy()
    expect(b.queryActivity.mock.calls[1][0]).toMatchObject({ offset: 50 })
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull()
  })

  it("opens the details of a row, with its arguments", async () => {
    b.queryActivity.mockResolvedValue(
      pageOf([entry(1, { tool: "update_doc", status: "declined", confirm: "declined", runID: "run-7f3a", args: '{\n  "data": {\n    "status": "Closed"\n  }\n}', names: ["TD-0001", "TD-0002"], namesTotal: 5 })]),
    )
    open()
    fireEvent.click(await screen.findByRole("button", { name: /Updated a document/ }))
    const dialog = await screen.findByRole("dialog")
    expect(within(dialog).getByText("update_doc")).toBeTruthy()
    expect(within(dialog).getByText("run-7f3a")).toBeTruthy()
    expect(within(dialog).getByText(/and 3 more/)).toBeTruthy()
    expect(within(dialog).getByText(/"status": "Closed"/)).toBeTruthy()
    expect(within(dialog).getAllByText("Declined").length).toBeGreaterThan(0)
  })

  it("explains an empty log", async () => {
    b.queryActivity.mockResolvedValue(pageOf([], { exists: false, sites: [], clients: [], statuses: [] }))
    open()
    expect(await screen.findByText("No activity yet")).toBeTruthy()
    expect(screen.getByText(/after the Assistant or a connected app uses one of your sites/)).toBeTruthy()
    expect(screen.queryByLabelText("App")).toBeNull()
  })

  it("says nothing matches when filters match nothing, and clears them", async () => {
    open()
    await screen.findByRole("list", { name: "Tool calls" })
    b.queryActivity.mockResolvedValue(pageOf([], { total: 0 }))
    fireEvent.change(screen.getByLabelText("Result"), { target: { value: "denied" } })
    expect(await screen.findByText("Nothing matches these filters.")).toBeTruthy()
    b.queryActivity.mockResolvedValue(pageOf([entry(1)]))
    const buttons = screen.getAllByRole("button", { name: "Clear filters" })
    fireEvent.click(buttons[buttons.length - 1])
    await waitFor(() => expect(screen.queryByText("Nothing matches these filters.")).toBeNull())
    expect(lastQuery()).toMatchObject({ status: "" })
  })

  it("notes lines that could not be read", async () => {
    b.queryActivity.mockResolvedValue(pageOf([entry(1)], { skipped: 2 }))
    open()
    expect(await screen.findByText("2 lines could not be read.")).toBeTruthy()
  })
})
