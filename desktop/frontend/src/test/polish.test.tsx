import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import "@/i18n"
import App from "@/App"
import { AppProvider } from "@/app/app-context"
import { ThemeProvider } from "@/app/theme"
import { Toaster } from "@/components/ui/toast"
import { TooltipProvider } from "@/components/ui/tooltip"
import { feedbackURL } from "@/lib/feedback"
import { backend } from "@/mock/backend"
import { AssistantScreen } from "@/screens/assistant/assistant-screen"

vi.mock("@/lib/backend", async () => await import("@/mock/backend"))

afterEach(cleanup)

// jsdom has no matchMedia (the theme and the sidebar ask it).
window.matchMedia ??= ((q: string) =>
  ({ matches: false, media: q, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }) as unknown as MediaQueryList)

beforeEach(async () => {
  for (const c of await backend.listConversations()) await backend.deleteConversation(c.id)
  try {
    localStorage.setItem("ffd-onboarded", "1")
  } catch {
    // jsdom has storage; nothing to do without it.
  }
})

const actions = { addSite: () => {}, connectAssistant: () => {}, installFFC: () => {}, newConversation: () => {}, takeNewConversation: () => {}, openShortcuts: () => {} }

describe("feedback link", () => {
  it("prefills only the version, the OS and the language", () => {
    const u = new URL(
      feedbackURL({ appVersion: "0.4.0", os: "windows", configPath: "C:\\x", configExists: true } as never),
    )
    expect(u.origin + u.pathname).toBe("https://github.com/nasroykh/foxmayn_frappe_cli/issues/new")
    expect(Object.fromEntries(u.searchParams)).toEqual({ template: "desktop-feedback.yml", lang: "en", version: "0.4.0", os: "Windows" })
  })
})

function renderApp() {
  return render(
    <ThemeProvider>
      <TooltipProvider>
        <Toaster>
          <App />
        </Toaster>
      </TooltipProvider>
    </ThemeProvider>,
  )
}

const closeDialog = async (name: string) => {
  fireEvent.keyDown(document.activeElement ?? window, { key: "Escape" })
  await waitFor(() => expect(screen.queryByRole("dialog", { name })).toBeNull())
}

describe("app shortcuts", () => {
  it("matches punctuation by character, so AZERTY and QWERTZ layouts work", async () => {
    renderApp()
    await screen.findAllByText("Sites")
    // AZERTY: "/" is Shift+":" (code Period); QWERTZ: Shift+7.
    fireEvent.keyDown(window, { key: "/", code: "Period", ctrlKey: true, shiftKey: true })
    expect(await screen.findByRole("dialog", { name: "Keyboard shortcuts" })).toBeTruthy()
    await closeDialog("Keyboard shortcuts")
    fireEvent.keyDown(window, { key: "/", code: "Digit7", ctrlKey: true, shiftKey: true })
    expect(await screen.findByRole("dialog", { name: "Keyboard shortcuts" })).toBeTruthy()
    await closeDialog("Keyboard shortcuts")
    // AZERTY: "," is the key in the place of QWERTY's M.
    fireEvent.keyDown(window, { key: ",", code: "KeyM", ctrlKey: true })
    expect(await screen.findByRole("heading", { level: 1, name: "Settings" })).toBeTruthy()
  }, 15000)

  it("ignores Ctrl+N and Ctrl+, while a dialog is open", async () => {
    renderApp()
    await screen.findAllByText("Sites")
    fireEvent.keyDown(window, { key: "/", code: "Slash", ctrlKey: true })
    await screen.findByRole("dialog", { name: "Keyboard shortcuts" })
    fireEvent.keyDown(window, { key: "n", code: "KeyN", ctrlKey: true })
    fireEvent.keyDown(window, { key: ",", code: "Comma", ctrlKey: true })
    await new Promise((r) => setTimeout(r, 300))
    expect(screen.queryByRole("dialog", { name: "New conversation" })).toBeNull()
    // The page behind the modal is hidden from the accessibility tree, hence hidden: true.
    expect(screen.getByRole("heading", { level: 1, name: "Sites", hidden: true })).toBeTruthy()
  }, 15000)

  it("opens the shortcuts sheet on Ctrl+/ and a new conversation on Ctrl+N, by physical key", async () => {
    renderApp()
    await screen.findAllByText("Sites")
    fireEvent.keyDown(window, { key: "/", code: "Slash", ctrlKey: true })
    expect(await screen.findByRole("dialog", { name: "Keyboard shortcuts" })).toBeTruthy()
    await closeDialog("Keyboard shortcuts")
    // An Arabic layout sends its own letter as key; the code is still KeyN.
    fireEvent.keyDown(window, { key: "ى", code: "KeyN", ctrlKey: true })
    expect(await screen.findByRole("dialog", { name: "New conversation" }, { timeout: 5000 })).toBeTruthy()
  }, 15000)
})

describe("empty conversation", () => {
  it("offers starter prompts that fill the composer without sending", async () => {
    await backend.newConversation("acme-prod", "read", "anthropic", "")
    const send = vi.spyOn(backend, "sendMessage")
    render(
      <AppProvider actions={actions} screen="assistant" setScreen={() => {}}>
        <AssistantScreen />
      </AppProvider>,
    )
    const prompts = await screen.findByRole("list", { name: "Try asking" }, { timeout: 5000 })
    const buttons = [...prompts.querySelectorAll("button")]
    expect(buttons).toHaveLength(4)
    const input = (await screen.findByLabelText("Message")) as HTMLTextAreaElement
    await waitFor(() => expect(buttons[0].disabled).toBe(false))
    for (const b of buttons) {
      fireEvent.click(b)
      expect(input.value).toBe(b.textContent)
    }
    expect(input.value).toBe("How many open ToDos are there, grouped by who they are assigned to?")
    // Nothing was sent.
    await new Promise((r) => setTimeout(r, 200))
    expect(send).not.toHaveBeenCalled()
    send.mockRestore()
  }, 15000)

  it("opens the delete confirmation on Delete in the conversation list", async () => {
    await backend.newConversation("acme-prod", "read", "anthropic", "")
    render(
      <AppProvider actions={actions} screen="assistant" setScreen={() => {}}>
        <AssistantScreen />
      </AppProvider>,
    )
    const row = await screen.findByRole("button", { current: true }, { timeout: 5000 })
    row.focus()
    fireEvent.keyDown(row, { key: "Delete" })
    expect(await screen.findByRole("alertdialog")).toBeTruthy()
  }, 15000)
})
