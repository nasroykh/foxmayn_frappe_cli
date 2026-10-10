import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import "@/i18n"
import { AppProvider } from "@/app/app-context"
import { toast } from "@/components/ui/toast"
import { backend } from "@/mock/backend"
import { AssistantScreen } from "@/screens/assistant/assistant-screen"

// The composer's attachments against the mock backend (its "file dialog"
// picks stock.csv, then prices.xlsx, then a PDF it refuses, in turn).
vi.mock("@/lib/backend", async () => await import("@/mock/backend"))

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

beforeEach(async () => {
  for (const c of await backend.listConversations()) await backend.deleteConversation(c.id)
})

const actions = { addSite: () => {}, connectAssistant: () => {}, installFFC: () => {}, newConversation: () => {}, takeNewConversation: () => {}, openShortcuts: () => {} }

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
    // The composer is a Wails drop target that names its conversation.
    const form = list.closest("form")!
    expect(form.hasAttribute("data-file-drop-target")).toBe(true)
    expect(form.getAttribute("data-conv-id")).toBe(c.id)

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
    // The mock's samples go on from the test before: a PDF, a DOCX, then a refusal.
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
    expect(staged()?.querySelectorAll("li")).toHaveLength(2)
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
