import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"

import "@/i18n"
import { SiteAssistantSettings } from "@/screens/assistant/site-settings"

const b = vi.hoisted(() => ({ getSiteSettings: vi.fn(), saveSiteSettings: vi.fn() }))
vi.mock("@/lib/backend", () => ({ backend: b }))
vi.mock("@/app/app-context", () => ({
  useApp: () => ({ sites: { data: { defaultSite: "acme-prod", sites: [{ name: "acme-prod" }] } } }),
}))

const on = { site: "acme-prod", url: "https://erp.acme.example", instructions: "", localOnly: true, localOnlyFrom: "" }

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

describe("Local models only", () => {
  it("asks before turning it off, and saves only after Allow online models", async () => {
    b.getSiteSettings.mockResolvedValue(on)
    b.saveSiteSettings.mockImplementation(async (s) => s)
    render(<SiteAssistantSettings />)
    const sw = await screen.findByRole("switch", { name: "Local models only" })
    await waitFor(() => expect(sw.getAttribute("aria-checked")).toBe("true"))
    fireEvent.click(sw)
    fireEvent.click(screen.getByRole("button", { name: "Save" }))

    const dialog = await screen.findByRole("alertdialog")
    expect(within(dialog).getByText("Let online models read this site?")).toBeTruthy()
    expect(b.saveSiteSettings).not.toHaveBeenCalled()
    await waitFor(() => expect(document.activeElement).toBe(within(dialog).getByRole("button", { name: "Cancel" })))
    fireEvent.click(within(dialog).getByRole("button", { name: "Allow online models" }))
    await waitFor(() => expect(b.saveSiteSettings).toHaveBeenCalledWith({ ...on, localOnly: false }))
  })

  it("saves without a question when it stays on", async () => {
    b.getSiteSettings.mockResolvedValue(on)
    b.saveSiteSettings.mockImplementation(async (s) => s)
    render(<SiteAssistantSettings />)
    await screen.findByRole("switch", { name: "Local models only" })
    fireEvent.click(screen.getByRole("button", { name: "Save" }))
    await waitFor(() => expect(b.saveSiteSettings).toHaveBeenCalled())
    expect(screen.queryByRole("alertdialog")).toBeNull()
  })
})
