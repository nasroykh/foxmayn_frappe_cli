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

const actions = { addSite: () => {}, connectAssistant: () => {}, installFFC: () => {}, newConversation: () => {}, takeNewConversation: () => {}, openShortcuts: () => {} }

function show() {
  return render(
    <AppProvider actions={actions} screen="assistant" setScreen={() => {}}>
      <AssistantScreen />
    </AppProvider>,
  )
}

/** The open conversation's title in the chat header. */
const chatTitle = () => document.querySelector("section > header h2")?.textContent

async function send(text: string) {
  const input = (await screen.findByLabelText("Message")) as HTMLTextAreaElement
  await waitFor(() => expect(input.disabled).toBe(false))
  fireEvent.change(input, { target: { value: text } })
  fireEvent.keyDown(input, { key: "Enter" })
}

describe("Assistant screen with the mock backend", () => {
  it("opens the conversation it just created, with the profile chosen", async () => {
    await backend.newConversation("acme-prod", "read", "anthropic", "")
    show()
    fireEvent.click(await screen.findByRole("button", { name: "New" }))
    // The chat header has a profile picker too: take the dialog's by id.
    await waitFor(() => expect(document.getElementById("newconv-profile")).toBeTruthy())
    const profile = document.getElementById("newconv-profile") as HTMLSelectElement
    await waitFor(() => expect(profile.options.length).toBeGreaterThan(1))
    fireEvent.change(document.getElementById("newconv-site")!, { target: { value: "acme-staging" } })
    fireEvent.change(profile, { target: { value: "accounts" } })
    fireEvent.click(screen.getByRole("button", { name: "Start" }))
    // The new conversation is the one on screen and the current one in the list.
    await waitFor(() => expect(document.querySelector("section > header")?.textContent).toContain("acme-staging"))
    const current = document.querySelector('nav li [aria-current="true"]')
    expect(current?.textContent).toContain("acme-staging")
    const c = (await backend.listConversations()).find((x) => x.site === "acme-staging")
    expect(c?.profileID).toBe("accounts")
    // Accounts helper may change things: the switch is on Ask before changes.
    expect(c?.mode).toBe("ask")
  })


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

  it("shows the cost of a run and the total, names the conversation, and lets the user rename it", async () => {
    await backend.newConversation("acme-prod", "read", "anthropic", "claude-sonnet-5-5")
    show()
    await send("What is TD-0001?")
    // The stored line under the answer, and the header total (which includes the title call).
    await waitFor(() => expect(document.querySelector('[data-cost-kind="exact"]')?.textContent).toContain("$0.0156"), { timeout: 12000 })
    await waitFor(() => expect(screen.getByTestId("usage-total").textContent).toMatch(/Total: .*\$0\.03/), { timeout: 8000 })
    // The model's title replaces the first words of the message.
    await waitFor(() => expect(chatTitle()).toBe("Open ToDos"), { timeout: 8000 })
    // The user's name stays.
    fireEvent.click(screen.getByRole("button", { name: "Rename this conversation" }))
    const field = screen.getByLabelText("Conversation name") as HTMLInputElement
    fireEvent.change(field, { target: { value: "My todos" } })
    fireEvent.click(screen.getByRole("button", { name: "Save name" }))
    await waitFor(() => expect(chatTitle()).toBe("My todos"), { timeout: 5000 })
    expect((await backend.listConversations())[0].title).toBe("My todos")
  }, 30000)

  it("saves a name even when it is unchanged, so confirming it locks it", async () => {
    await backend.newConversation("acme-prod", "read", "anthropic", "claude-sonnet-5-5")
    const rename = vi.spyOn(backend, "renameConversation")
    show()
    await send("What is TD-0001?")
    await waitFor(() => expect(chatTitle()).toBe("Open ToDos"), { timeout: 12000 })
    fireEvent.click(screen.getByRole("button", { name: "Rename this conversation" }))
    fireEvent.click(screen.getByRole("button", { name: "Save name" }))
    await waitFor(() => expect(rename).toHaveBeenCalledWith(expect.any(String), "Open ToDos"))
    rename.mockRestore()
  }, 30000)

  it("says cost unknown for a model that is not in the price table", async () => {
    await backend.newConversation("acme-prod", "read", "anthropic", "mystery-model")
    show()
    await send("What is TD-0001?")
    await waitFor(() => expect(document.querySelector('[data-cost-kind="unknown"]')?.textContent).toContain("cost unknown"), { timeout: 12000 })
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
