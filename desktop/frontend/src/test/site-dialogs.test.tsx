import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import "@/i18n"
import type { Site } from "@/lib/backend-types"
import { RemoveSiteDialog, SiteURLDialog } from "@/screens/site-dialogs"

const b = vi.hoisted(() => ({
  needsSignIn: vi.fn(),
  changeURL: vi.fn(),
  getPolicy: vi.fn(),
  remove: vi.fn(),
}))
vi.mock("@/lib/backend", () => ({ backend: b }))
vi.mock("@/app/app-context", () => ({ useApp: () => ({ reloadSites: async () => {}, reloadAssistants: async () => {} }) }))

const apiSite = { name: "acme-prod", url: "https://erp.acme.example", auth: "apikey", isDefault: true, plainHTTP: false } as Site
const pwSite = { ...apiSite, auth: "password" } as Site
const blank = { readOnly: false, confirm: "", allowTools: [], allowDoctypes: [], denyDoctypes: [], allowMethods: [], denyMethods: [] }
const empty = { apiKey: "", apiSecret: "", username: "", password: "" }

beforeEach(() => {
  b.needsSignIn.mockResolvedValue(false)
  b.getPolicy.mockResolvedValue(blank)
})
afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

async function typeURL(url: string) {
  const input = (await screen.findByLabelText("Site address")) as HTMLInputElement
  fireEvent.change(input, { target: { value: url } })
}

describe("change address", () => {
  it("on the same server asks nothing and sends no sign-in", async () => {
    b.changeURL.mockResolvedValue("https://erp2.acme.example")
    render(<SiteURLDialog site={apiSite} onClose={() => {}} />)
    await typeURL("https://erp2.acme.example")
    await waitFor(() => expect(b.needsSignIn).toHaveBeenCalledWith("acme-prod", "https://erp2.acme.example"))
    expect(screen.queryByTestId("url-sign-in")).toBeNull()
    fireEvent.click(screen.getByRole("button", { name: "Save address" }))
    await waitFor(() => expect(b.changeURL).toHaveBeenCalledWith("acme-prod", "https://erp2.acme.example", empty))
  })

  it("on another server shows the API key fields and passes them", async () => {
    b.needsSignIn.mockResolvedValue(true)
    b.changeURL.mockResolvedValue("https://other.example")
    render(<SiteURLDialog site={apiSite} onClose={() => {}} />)
    await typeURL("https://other.example")
    const fields = await screen.findByTestId("url-sign-in")
    expect(within(fields).getByText(/not sent there/)).toBeTruthy()
    const save = screen.getByRole("button", { name: "Save address" }) as HTMLButtonElement
    expect(save.disabled).toBe(true)
    fireEvent.change(within(fields).getByLabelText("API key"), { target: { value: "k1" } })
    fireEvent.change(within(fields).getByLabelText("API secret"), { target: { value: "s1" } })
    await waitFor(() => expect(save.disabled).toBe(false))
    fireEvent.click(save)
    await waitFor(() =>
      expect(b.changeURL).toHaveBeenCalledWith("acme-prod", "https://other.example", { ...empty, apiKey: "k1", apiSecret: "s1" }),
    )
  })

  it("asks for username and password on a password site, and shows the fields on site.newServer", async () => {
    b.needsSignIn.mockResolvedValue(false)
    b.changeURL.mockRejectedValueOnce({ code: "invalid", message: "new server", key: "site.newServer" })
    render(<SiteURLDialog site={pwSite} onClose={() => {}} />)
    await typeURL("https://other.example")
    fireEvent.click(screen.getByRole("button", { name: "Save address" }))
    const fields = await screen.findByTestId("url-sign-in")
    expect(within(fields).getByLabelText("Username or email")).toBeTruthy()
    expect(within(fields).getByLabelText("Password")).toBeTruthy()
  })
})

describe("remove site", () => {
  it("says the safety rules go with the site when it has some", async () => {
    b.getPolicy.mockResolvedValue({ ...blank, readOnly: true })
    render(<RemoveSiteDialog site={apiSite} onClose={() => {}} />)
    expect(await screen.findByText(/Its safety rules are deleted with it\./)).toBeTruthy()
    expect(screen.getByRole("alertdialog").querySelector("bdi")?.textContent).toBe("acme-prod")
  })

  it("does not mention rules when there are none", async () => {
    render(<RemoveSiteDialog site={apiSite} onClose={() => {}} />)
    await screen.findByRole("alertdialog")
    await waitFor(() => expect(b.getPolicy).toHaveBeenCalled())
    expect(screen.queryByText(/safety rules/)).toBeNull()
  })
})
