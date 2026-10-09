import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import "@/i18n"
import { toast } from "@/components/ui/toast"
import type { Conversation, Profile } from "@/lib/backend-types"
import { ProfilePicker } from "@/screens/assistant/profile-picker"
import { blankProfile, ProfileSettings } from "@/screens/assistant/profile-settings"

const b = vi.hoisted(() => ({
  listPresets: vi.fn(),
  listProfiles: vi.fn(),
  saveProfile: vi.fn(),
  deleteProfile: vi.fn(),
  setConversationProfile: vi.fn(),
  promptPreview: vi.fn(),
}))
vi.mock("@/lib/backend", () => ({ backend: b }))

const explore: Profile = {
  ...blankProfile(),
  id: "explore",
  name: "Explore",
  preset: true,
  mode: "read",
  instructions: "Explore the site.",
}
const accounts: Profile = { ...blankProfile(), id: "accounts", name: "Accounts helper", preset: true, mode: "ask", toolsets: ["core", "erp"] }
const mine: Profile = { ...blankProfile(), id: "p1", name: "Mine", mode: "read" }

const conv: Conversation = {
  id: "c1",
  title: "t",
  site: "acme",
  mode: "ask",
  providerID: "p",
  model: "m",
  profileID: "",
  created: "",
  updated: "",
}

beforeEach(() => {
  b.listPresets.mockResolvedValue([explore, accounts])
  b.listProfiles.mockResolvedValue([mine])
})
afterEach(() => {
  cleanup()
  vi.clearAllMocks()
  vi.restoreAllMocks()
})

describe("profile picker", () => {
  it("lists the presets and the user's profiles and switches the conversation", async () => {
    const changed = { ...conv, profileID: "explore" }
    b.setConversationProfile.mockResolvedValue(changed)
    const onChanged = vi.fn()
    render(<ProfilePicker conv={conv} disabled={false} onChanged={onChanged} />)
    const select = (await screen.findByLabelText("Profile")) as HTMLSelectElement
    const names = Array.from(select.options).map((o) => o.textContent)
    expect(names).toEqual(["No profile", "Explore", "Accounts helper", "Mine"])
    fireEvent.change(select, { target: { value: "explore" } })
    await waitFor(() => expect(onChanged).toHaveBeenCalledWith(changed))
    expect(b.setConversationProfile).toHaveBeenCalledWith("c1", "explore")
  })

  it("shows the refusal when the switch is refused", async () => {
    const add = vi.spyOn(toast, "add")
    b.setConversationProfile.mockRejectedValue({ code: "invalid", message: "This site is set to use local models only." })
    const onChanged = vi.fn()
    render(<ProfilePicker conv={conv} disabled={false} onChanged={onChanged} />)
    fireEvent.change(await screen.findByLabelText("Profile"), { target: { value: "accounts" } })
    await waitFor(() => expect(add).toHaveBeenCalled())
    expect(add.mock.calls[0][0].description).toContain("local models only")
    expect(onChanged).not.toHaveBeenCalled()
  })

  it("says the profile keeps an asking conversation read only", async () => {
    render(<ProfilePicker conv={{ ...conv, profileID: "explore" }} disabled={false} onChanged={vi.fn()} />)
    expect(await screen.findByText("Read only by profile")).toBeTruthy()
  })

  it("cannot change while a run is active", async () => {
    render(<ProfilePicker conv={conv} disabled onChanged={vi.fn()} />)
    expect(((await screen.findByLabelText("Profile")) as HTMLSelectElement).disabled).toBe(true)
  })
})

describe("profile editor", () => {
  it("offers presets only as a copy", async () => {
    render(<ProfileSettings />)
    expect(await screen.findByRole("button", { name: "Duplicate Explore" })).toBeTruthy()
    expect(screen.queryByRole("button", { name: "Edit Explore" })).toBeNull()
    expect(screen.queryByRole("button", { name: "Delete Explore" })).toBeNull()
    expect(await screen.findByRole("button", { name: "Edit Mine" })).toBeTruthy()
  })

  it("duplicates a preset into a new profile of its own", async () => {
    b.saveProfile.mockImplementation(async (p: Profile) => ({ ...p, id: "new" }))
    render(<ProfileSettings />)
    fireEvent.click(await screen.findByRole("button", { name: "Duplicate Accounts helper" }))
    const name = (await screen.findByLabelText("Name")) as HTMLInputElement
    expect(name.value).toBe("Accounts helper (copy)")
    fireEvent.click(screen.getByRole("button", { name: "Save" }))
    await waitFor(() => expect(b.saveProfile).toHaveBeenCalledTimes(1))
    const sent = b.saveProfile.mock.calls[0][0] as Profile
    expect(sent).toMatchObject({ id: "", preset: false, basedOn: "accounts", mode: "ask", toolsets: ["core", "erp"] })
    await waitFor(() => expect(b.listProfiles).toHaveBeenCalledTimes(2))
  })

  it("starts a new profile read only and sends the lists it was given", async () => {
    b.saveProfile.mockImplementation(async (p: Profile) => ({ ...p, id: "new" }))
    render(<ProfileSettings />)
    fireEvent.click(await screen.findByRole("button", { name: "New profile" }))
    fireEvent.change(await screen.findByLabelText("Name"), { target: { value: "Narrow" } })
    fireEvent.change(screen.getByLabelText("Tools to hide"), { target: { value: "delete_doc, bulk_delete" } })
    fireEvent.change(screen.getByLabelText("Step limit (1 to 100)"), { target: { value: "10" } })
    fireEvent.click(screen.getByRole("button", { name: "Save" }))
    await waitFor(() => expect(b.saveProfile).toHaveBeenCalledTimes(1))
    expect(b.saveProfile.mock.calls[0][0]).toMatchObject({
      name: "Narrow",
      mode: "read",
      callMethod: false,
      denyTools: ["delete_doc", "bulk_delete"],
      stepLimit: 10,
    })
  })

  it("keeps the editor open with the error when saving is refused", async () => {
    b.saveProfile.mockRejectedValue({ code: "invalid", message: "Built-in profiles cannot be changed.", field: "id" })
    render(<ProfileSettings />)
    fireEvent.click(await screen.findByRole("button", { name: "Edit Mine" }))
    fireEvent.change(await screen.findByLabelText("Name"), { target: { value: "Explore" } })
    fireEvent.click(screen.getByRole("button", { name: "Save" }))
    expect((await screen.findByRole("alert")).textContent).toContain("Built-in profiles cannot be changed.")
    expect(screen.getByLabelText("Name")).toBeTruthy()
  })
})
