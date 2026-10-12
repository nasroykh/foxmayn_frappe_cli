import { cleanup, createEvent, fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import "@/i18n"
import { AppProvider } from "@/app/app-context"
import { toast } from "@/components/ui/toast"
import { backend, resetMockSamples } from "@/mock/backend"
import { AssistantScreen } from "@/screens/assistant/assistant-screen"

// The composer's attachments against the mock backend (its "file dialog"
// picks stock.csv, then prices.xlsx, then a PDF it refuses, in turn).
vi.mock("@/lib/backend", async () => await import("@/mock/backend"))

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

beforeEach(async () => {
  // Each test starts from the first sample (CSV, XLSX, PDF, DOCX, refusal).
  resetMockSamples()
  for (const c of await backend.listConversations()) await backend.deleteConversation(c.id)
})

const actions = { addSite: () => {}, connectAssistant: () => {}, installFFC: () => {}, newConversation: () => {}, takeNewConversation: () => {}, openShortcuts: () => {}, openActivity: () => {}, takeActivityFilter: () => {} }

function show() {
  return render(
    <AppProvider actions={actions} screen="assistant" setScreen={() => {}}>
      <AssistantScreen />
    </AppProvider>,
  )
}

const staged = () => screen.queryByRole("list", { name: "Files for the next message" })

async function attach() {
  const button = await screen.findByRole("button", { name: /^Attach files/ })
  await waitFor(() => expect((button as HTMLButtonElement).disabled).toBe(false))
  fireEvent.click(button)
}

function paste(input: HTMLElement, file: File) {
  fireEvent.paste(input, { clipboardData: { files: [file], types: ["Files"] } })
}

describe("composer attachments", () => {
  it("stages a file as a chip, removes it, and sends a message with only an attachment", async () => {
    const c = await backend.newConversation("acme-prod", "read", "anthropic", "")
    show()
    await attach()
    const list = await waitFor(() => {
      const l = staged()
      expect(l).toBeTruthy()
      return l!
    })
    expect(within(list).getByText("stock.csv")).toBeTruthy()
    // The Wails file drop is off: the composer takes drops itself.
    const form = list.closest("form")!
    expect(form.hasAttribute("data-file-drop-target")).toBe(false)
    expect(form.hasAttribute("data-conv-id")).toBe(false)

    fireEvent.click(within(list).getByRole("button", { name: "Remove stock.csv" }))
    await waitFor(() => expect(staged()).toBeNull())
    expect(await backend.listAttachments(c.id)).toEqual([])

    await attach()
    await waitFor(() => expect(staged()?.textContent).toContain("prices.xlsx"))
    const send = screen.getByRole("button", { name: "Send message" }) as HTMLButtonElement
    expect(send.disabled).toBe(false) // no text, one file
    fireEvent.click(send)
    await waitFor(() => expect(staged()).toBeNull())
    await waitFor(async () => {
      const d = await backend.getConversation(c.id)
      expect(d.messages[0]?.attachments?.map((a) => a.name)).toEqual(["prices.xlsx"])
    })
    // The sent message shows its file, without a remove button.
    const sent = await screen.findByRole("list", { name: "Attached files" }, { timeout: 8000 })
    expect(within(sent).getByText("prices.xlsx")).toBeTruthy()
    expect(within(sent).queryByRole("button")).toBeNull()
  }, 20000)

  it("stages a PDF and a Word file, and shows a refused file as a toast", async () => {
    await backend.newConversation("acme-prod", "read", "anthropic", "")
    const add = vi.spyOn(toast, "add")
    show()
    // The samples in order: CSV, XLSX, then a PDF, a DOCX and a refusal.
    await attach()
    await attach()
    await waitFor(() => expect(staged()?.textContent).toContain("prices.xlsx"))
    await attach()
    await waitFor(() => expect(staged()?.textContent).toContain("invoice.pdf"))
    await attach()
    await waitFor(() => expect(staged()?.textContent).toContain("memo.docx"))
    await attach()
    await waitFor(() =>
      expect(add).toHaveBeenCalledWith(
        expect.objectContaining({ title: "Not attached", description: expect.stringContaining("cannot be attached") }),
      ),
    )
    expect(staged()?.querySelectorAll("li")).toHaveLength(4)
  })

  it("stages a pasted image and refuses one over 5 MB", async () => {
    const c = await backend.newConversation("acme-prod", "read", "anthropic", "")
    const add = vi.spyOn(toast, "add")
    show()
    const input = await screen.findByLabelText("Message")
    paste(input, new File([new Uint8Array([0x89, 0x50, 0x4e, 0x47])], "shot.png", { type: "image/png" }))
    await waitFor(() => expect(staged()?.textContent).toContain("pasted-image.png"))
    expect((await backend.listAttachments(c.id)).map((a) => a.kind)).toEqual(["image"])

    paste(input, new File([new Uint8Array((5 << 20) + 1)], "big.png", { type: "image/png" }))
    await waitFor(() =>
      expect(add).toHaveBeenCalledWith(expect.objectContaining({ description: "The pasted image is larger than 5 MB." })),
    )
    expect(await backend.listAttachments(c.id)).toHaveLength(1)

    // Pasted text is not an attachment.
    fireEvent.paste(input, { clipboardData: { files: [], types: ["text/plain"] } })
    expect(await backend.listAttachments(c.id)).toHaveLength(1)
  })
})

function fileDrop(files: File[], types: string[] = ["Files"]) {
  return { dataTransfer: { files, types, dropEffect: "none" } }
}

describe("composer drops", () => {
  it("reads each dropped file and stages it through addDroppedFile with its base64", async () => {
    const c = await backend.newConversation("acme-prod", "read", "anthropic", "")
    const add = vi.spyOn(backend, "addDroppedFile")
    show()
    const input = await screen.findByLabelText("Message")
    const form = input.closest("form")!

    // A drag of files is accepted as a copy and shows the drop state.
    const over = createEvent.dragOver(form, fileDrop([]))
    fireEvent(form, over)
    expect(over.defaultPrevented).toBe(true)
    expect((over as unknown as { dataTransfer: { dropEffect: string } }).dataTransfer.dropEffect).toBe("copy")
    expect(await screen.findByText("Drop files here to attach them")).toBeTruthy()

    const bytes = new TextEncoder().encode("sku,qty,A1,3")
    const file = new File([bytes], "stock.csv", { type: "text/csv" })
    const drop = createEvent.drop(form, fileDrop([file]))
    fireEvent(form, drop)
    expect(drop.defaultPrevented).toBe(true)

    await waitFor(() => expect(staged()?.textContent).toContain("stock.csv"))
    const b64 = btoa(String.fromCharCode(...bytes))
    expect(add).toHaveBeenCalledWith(c.id, "stock.csv", b64)
    expect((await backend.listAttachments(c.id)).map((a) => a.name)).toEqual(["stock.csv"])
    expect(screen.queryByText("Drop files here to attach them")).toBeNull()
  })

  it("shows the refusal of a dropped file as a toast and ignores drags without files", async () => {
    const c = await backend.newConversation("acme-prod", "read", "anthropic", "")
    const toasts = vi.spyOn(toast, "add")
    const add = vi.spyOn(backend, "addDroppedFile")
    show()
    const form = (await screen.findByLabelText("Message")).closest("form")!

    const text = createEvent.dragOver(form, fileDrop([], ["text/plain"]))
    fireEvent(form, text)
    expect(text.defaultPrevented).toBe(false)
    fireEvent.drop(form, fileDrop([new File(["x"], "a.csv")], ["text/plain"]))
    expect(add).not.toHaveBeenCalled()

    fireEvent.drop(form, fileDrop([new File(["MZ"], "setup.exe")]))
    await waitFor(() =>
      expect(toasts).toHaveBeenCalledWith(expect.objectContaining({ description: expect.stringContaining("setup.exe") })),
    )
    expect(await backend.listAttachments(c.id)).toEqual([])
  })
})
