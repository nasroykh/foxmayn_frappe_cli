import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import "@/i18n"
import { AppProvider } from "@/app/app-context"
import { backend } from "@/mock/backend"
import { AssistantScreen } from "@/screens/assistant/assistant-screen"

// The real screen against the scripted mock assistant.
vi.mock("@/lib/backend", async () => await import("@/mock/backend"))

afterEach(cleanup)

// One conversation per test: the mock keeps its state between them.
beforeEach(async () => {
  for (const c of await backend.listConversations()) await backend.deleteConversation(c.id)
})

const actions = { addSite: () => {}, connectAssistant: () => {}, installFFC: () => {} }

function show() {
  return render(
    <AppProvider actions={actions} screen="assistant" setScreen={() => {}}>
      <AssistantScreen />
    </AppProvider>,
  )
}

async function send(text: string) {
  const input = (await screen.findByLabelText("Message")) as HTMLTextAreaElement
  await waitFor(() => expect(input.disabled).toBe(false))
  fireEvent.change(input, { target: { value: text } })
  fireEvent.keyDown(input, { key: "Enter" })
}

describe("Assistant screen with the mock backend", () => {
  it("streams a read answer with a tool row, then shows the stored conversation", async () => {
    await backend.newConversation("acme-prod", "read", "anthropic", "")
    show()
    await send("What is TD-0001?")
    await waitFor(() => expect(screen.getAllByText("What is TD-0001?").length).toBeGreaterThan(0))
    await waitFor(() => expect(screen.getByText("get_doc")).toBeTruthy(), { timeout: 5000 })
    await waitFor(() => expect(document.body.textContent).toContain("TD-0001"), { timeout: 5000 })
    await waitFor(() => expect(screen.getByRole("button", { name: "Send message" })).toBeTruthy(), { timeout: 8000 })
  }, 15000)

  it("shows the approval card, takes Approve and removes the card", async () => {
    const c = await backend.newConversation("acme-prod", "ask", "anthropic", "")
    // The newest conversation is the one on screen.
    expect(c.id).toBeTruthy()
    show()
    await send("update the todo")
    const decline = await screen.findByRole("button", { name: "Decline" }, { timeout: 5000 })
    expect(document.activeElement).toBe(decline)
    expect(screen.getByText("The assistant wants to make a change")).toBeTruthy()
    expect(screen.getByLabelText("Field changes").textContent).toContain("description")
    // Mode cannot change while the run waits.
    expect(screen.getByRole("group", { name: "What the assistant may do" }).querySelector("button")?.hasAttribute("disabled")).toBe(true)
    fireEvent.click(screen.getByRole("button", { name: "Approve" }))
    await waitFor(() => expect(screen.queryByRole("button", { name: "Decline" })).toBeNull(), { timeout: 5000 })
    await waitFor(() => expect(screen.getByRole("button", { name: "Send message" })).toBeTruthy(), { timeout: 8000 })
  }, 20000)

  it("shows the error alert for a failing run", async () => {
    await backend.newConversation("acme-prod", "read", "anthropic", "")
    show()
    await send("this will fail")
    expect((await screen.findByRole("alert", {}, { timeout: 5000 })).textContent).toContain("The AI provider returned an error.")
  }, 15000)

  it("offers Continue when a run pauses", async () => {
    await backend.newConversation("acme-prod", "read", "anthropic", "")
    show()
    await send("check many todos")
    fireEvent.click(await screen.findByRole("button", { name: "Continue" }, { timeout: 8000 }))
    await waitFor(() => expect(screen.queryByRole("button", { name: "Continue" })).toBeNull(), { timeout: 8000 })
  }, 20000)
})
