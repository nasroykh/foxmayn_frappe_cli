import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import "@/i18n"
import type { ProviderInfo, Site } from "@/lib/backend-types"
import { AssistantOnboarding } from "@/screens/assistant/assistant-onboarding"

const b = vi.hoisted(() => ({
  detectLocal: vi.fn(),
  saveProvider: vi.fn(),
  setKey: vi.fn(),
  keyStatus: vi.fn(),
  listModels: vi.fn(),
  newConversation: vi.fn(),
}))
vi.mock("@/lib/backend", () => ({ backend: b }))

const anthropic: ProviderInfo = {
  id: "anthropic",
  kind: "anthropic",
  label: "Anthropic",
  baseURL: "",
  defaultModel: "claude-sonnet-5-5",
  keySet: false,
  keyLast4: "",
}

const sites = [{ name: "acme-prod", url: "https://acme.example" }] as unknown as Site[]

function show(providers: ProviderInfo[] = []) {
  return render(<AssistantOnboarding sites={sites} providers={providers} onProvidersChanged={() => {}} onDone={() => {}} />)
}

beforeEach(() => {
  b.detectLocal.mockResolvedValue([])
  b.saveProvider.mockImplementation(async (p: ProviderInfo) => ({
    ...p,
    id: p.id || p.kind,
    defaultModel: p.defaultModel || "claude-sonnet-5-5",
  }))
  b.keyStatus.mockResolvedValue({ set: false })
  b.listModels.mockResolvedValue([
    { id: "claude-opus-5-5", label: "Claude Opus 5.5", default: false },
    { id: "claude-sonnet-5-5", label: "Claude Sonnet 5.5", default: true },
  ])
})
afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

async function toKeyStep() {
  show()
  fireEvent.click(await screen.findByRole("button", { name: /Anthropic/ }))
  return (await screen.findByLabelText(/API key/)) as HTMLInputElement
}

describe("assistant onboarding", () => {
  it("offers the providers when there are none, and says when no local server is found", async () => {
    show()
    expect(screen.getByRole("button", { name: /Anthropic/ })).toBeTruthy()
    expect(screen.getByRole("button", { name: /OpenRouter/ })).toBeTruthy()
    expect(screen.getByRole("button", { name: /Another server/ })).toBeTruthy()
    expect(await screen.findByText(/No Ollama or LM Studio found/)).toBeTruthy()
  })

  it("offers a detected local server", async () => {
    b.detectLocal.mockResolvedValue([
      { ...anthropic, id: "ollama", kind: "ollama", label: "Ollama", baseURL: "http://localhost:11434/v1" },
    ])
    show()
    expect(await screen.findByRole("button", { name: /Ollama \(found on this computer\)/ })).toBeTruthy()
  })

  it("takes the key in a password field and shows the auth error when it is rejected", async () => {
    b.setKey.mockRejectedValue({ cause: { code: "auth", message: "The provider did not accept the API key." } })
    const input = await toKeyStep()
    expect(input.type).toBe("password")
    fireEvent.change(input, { target: { value: "bad-key" } })
    fireEvent.click(screen.getByRole("button", { name: "Save key" }))
    expect((await screen.findByRole("alert")).textContent).toContain("The provider did not accept the API key.")
    expect(b.setKey).toHaveBeenCalledWith("anthropic", "bad-key")
    expect(screen.getByRole("button", { name: "Next" }).hasAttribute("disabled")).toBe(true)
  })

  it("clears the field after a save and shows only the end of the key", async () => {
    b.setKey.mockResolvedValue(undefined)
    b.keyStatus.mockResolvedValueOnce({ set: false }).mockResolvedValue({ set: true, last4: "0123" })
    const input = await toKeyStep()
    fireEvent.change(input, { target: { value: "mock-key-WXYZ-0123" } })
    fireEvent.click(screen.getByRole("button", { name: "Save key" }))
    expect(await screen.findByText("Saved, ends in ••••0123")).toBeTruthy()
    expect(input.value).toBe("")
    expect(document.body.textContent).not.toContain("mock-key")
    await waitFor(() => expect(screen.getByRole("button", { name: "Next" }).hasAttribute("disabled")).toBe(false))
  })
})
