import { cleanup, fireEvent, render, screen } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import "@/i18n"
import type { ProviderInfo } from "@/lib/backend-types"
import { ProviderSettings } from "@/screens/assistant/provider-settings"

const b = vi.hoisted(() => ({
  listProviders: vi.fn(),
  saveProvider: vi.fn(),
  keyStatus: vi.fn(),
  setKey: vi.fn(),
  listModels: vi.fn(),
  detectLocal: vi.fn(),
  deleteProvider: vi.fn(),
}))
vi.mock("@/lib/backend", () => ({ backend: b }))

const custom: ProviderInfo = {
  id: "custom",
  kind: "custom",
  label: "My server",
  baseURL: "https://llm.example.com/v1",
  defaultModel: "m1",
  keySet: true,
  keyLast4: "9999",
}

beforeEach(() => {
  b.listProviders.mockResolvedValue([custom])
  b.keyStatus.mockResolvedValue({ set: false })
  b.listModels.mockResolvedValue([{ id: "m1", label: "m1", default: true }])
})
afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

describe("provider settings", () => {
  it("lists a provider with the end of its key", async () => {
    render(<ProviderSettings />)
    expect(await screen.findByText("My server")).toBeTruthy()
    expect(screen.getByText("Saved, ends in ••••9999")).toBeTruthy()
  })

  it("tells the user the key was removed when the address changed, and asks for it again", async () => {
    b.saveProvider.mockResolvedValue({ ...custom, baseURL: "https://other.example.com/v1", keySet: false, keyLast4: "", keyCleared: true })
    render(<ProviderSettings />)
    fireEvent.click(await screen.findByRole("button", { name: "Edit My server" }))
    fireEvent.change(await screen.findByLabelText("Server address"), { target: { value: "https://other.example.com/v1" } })
    fireEvent.click(screen.getByRole("button", { name: "Save" }))
    expect((await screen.findAllByRole("alert"))[0].textContent).toContain(
      "The address changed, so the saved key was removed. Enter it again.",
    )
    expect(await screen.findByLabelText(/API key/)).toBeTruthy()
  })
})
