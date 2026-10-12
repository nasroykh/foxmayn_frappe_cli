import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import "@/i18n"
import type { Site, SitePolicy } from "@/lib/backend-types"
import { SafetySheet } from "@/screens/safety-sheet"

const b = vi.hoisted(() => ({
  getPolicy: vi.fn(),
  previewPolicy: vi.fn(),
  savePolicy: vi.fn(),
}))
vi.mock("@/lib/backend", () => ({ backend: b }))

const site: Site = { name: "acme-prod", url: "https://erp.acme.example", auth: "oauth", isDefault: true, plainHTTP: false } as Site
const blank: SitePolicy = { readOnly: false, confirm: "", allowTools: [], allowDoctypes: [], denyDoctypes: [], allowMethods: [], denyMethods: [] }
const none = { opens: [], narrows: [] }

function open() {
  const onClose = vi.fn()
  render(<SafetySheet site={site} onClose={onClose} />)
  return onClose
}
const radio = (name: RegExp | string) => screen.findByRole("radio", { name }) as Promise<HTMLInputElement>

beforeEach(() => {
  b.getPolicy.mockResolvedValue(blank)
  b.previewPolicy.mockResolvedValue(none)
})
afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

describe("safety rules sheet", () => {
  it("shows the current policy as the right choice card", async () => {
    open()
    expect(await screen.findByText("Safety rules for acme-prod")).toBeTruthy()
    const ask = await radio(/Ask before risky changes/)
    await waitFor(() => expect(ask.getAttribute("aria-checked") ?? String(ask.checked)).toBe("true"))
    expect(screen.queryByTestId("safety-advanced")).toBeNull()
  })

  it("saves Read only with the loaded policy as base", async () => {
    b.savePolicy.mockResolvedValue({ ...blank, readOnly: true })
    const onClose = open()
    fireEvent.click(await radio(/Read only/))
    fireEvent.click(screen.getByRole("button", { name: "Save" }))
    await waitFor(() => expect(b.savePolicy).toHaveBeenCalled())
    expect(b.savePolicy).toHaveBeenCalledWith("acme-prod", blank, { ...blank, readOnly: true }, false)
    await waitFor(() => expect(onClose).toHaveBeenCalled())
  })

  it("keeps the lists when a site is tightened to Read only", async () => {
    const strict = { ...blank, denyDoctypes: ["User"], allowMethods: ["x.y"] }
    b.getPolicy.mockResolvedValue(strict)
    b.savePolicy.mockResolvedValue({ ...strict, readOnly: true })
    open()
    fireEvent.click(await radio(/Read only/))
    fireEvent.click(screen.getByRole("button", { name: "Save" }))
    await waitFor(() => expect(b.savePolicy).toHaveBeenCalled())
    expect(b.savePolicy).toHaveBeenCalledWith("acme-prod", strict, { ...strict, readOnly: true }, false)
  })

  it("opens a policy with lists as Custom", async () => {
    b.getPolicy.mockResolvedValue({ ...blank, denyDoctypes: ["User"] })
    open()
    expect(await screen.findByTestId("safety-advanced")).toBeTruthy()
    expect((screen.getByLabelText("Document types to refuse") as HTMLTextAreaElement).value).toBe("User")
  })

  it("asks in a dialog when the change opens access, and saves only after Open access", async () => {
    b.getPolicy.mockResolvedValue({ ...blank, readOnly: true })
    b.previewPolicy.mockResolvedValue({ opens: [{ kind: "readOnlyOff", values: [] }], narrows: [] })
    b.savePolicy.mockResolvedValue(blank)
    const onClose = open()
    fireEvent.click(await radio(/Ask before risky changes/))
    fireEvent.click(screen.getByRole("button", { name: "Save" }))
    const dialog = await screen.findByRole("alertdialog")
    expect(within(dialog).getByText("Read only is turned off.")).toBeTruthy()
    expect(within(dialog).getByText("acme-prod").tagName).toBe("BDI")
    expect(b.savePolicy).not.toHaveBeenCalled()
    // Cancel is the default focus.
    await waitFor(() => expect(document.activeElement).toBe(within(dialog).getByRole("button", { name: "Cancel" })))

    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }))
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull())
    expect(b.savePolicy).not.toHaveBeenCalled()
    expect(onClose).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole("button", { name: "Save" }))
    fireEvent.click(await within(await screen.findByRole("alertdialog")).findByRole("button", { name: "Open access" }))
    await waitFor(() => expect(b.savePolicy).toHaveBeenCalledWith("acme-prod", { ...blank, readOnly: true }, blank, true))
    await waitFor(() => expect(onClose).toHaveBeenCalled())
  })

  it("reloads the policy when the rules changed elsewhere", async () => {
    b.savePolicy.mockRejectedValue({ code: "invalid", message: "changed", key: "policy.changed" })
    open()
    fireEvent.click(await radio(/Read only/))
    b.getPolicy.mockResolvedValue({ ...blank, readOnly: true })
    fireEvent.click(screen.getByRole("button", { name: "Save" }))
    expect(await screen.findByText(/now the current ones/)).toBeTruthy()
    expect(b.getPolicy).toHaveBeenCalledTimes(2)
    const read = await radio(/Read only/)
    await waitFor(() => expect(read.getAttribute("aria-checked") ?? String(read.checked)).toBe("true"))
  })
})
