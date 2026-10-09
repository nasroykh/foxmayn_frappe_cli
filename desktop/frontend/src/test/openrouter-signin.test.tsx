import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import "@/i18n"
import type { OpenRouterAuth, ProviderInfo, Site } from "@/lib/backend-types"
import { AssistantOnboarding } from "@/screens/assistant/assistant-onboarding"
import { OpenRouterSignIn } from "@/screens/assistant/provider-parts"
import { ProviderSettings } from "@/screens/assistant/provider-settings"

const b = vi.hoisted(() => ({
  listProviders: vi.fn(),
  saveProvider: vi.fn(),
  keyStatus: vi.fn(),
  setKey: vi.fn(),
  listModels: vi.fn(),
  detectLocal: vi.fn(),
  deleteProvider: vi.fn(),
  newConversation: vi.fn(),
  signInOpenRouter: vi.fn(),
  cancelOpenRouterSignIn: vi.fn(),
  onOpenRouterAuth: vi.fn(),
  copyText: vi.fn(),
}))
vi.mock("@/lib/backend", () => ({ backend: b }))

const openrouter: ProviderInfo = {
  id: "openrouter",
  kind: "openrouter",
  label: "OpenRouter",
  baseURL: "https://openrouter.ai/api/v1",
  defaultModel: "",
  keySet: false,
  keyLast4: "",
}
const signedIn: ProviderInfo = { ...openrouter, keySet: true, keyLast4: "c0de" }

// The step line (the spinner is a status of its own).
const statusText = () => screen.getAllByRole("status").map((el) => el.textContent ?? "").join(" ")

let listeners: ((ev: OpenRouterAuth) => void)[] = []
// The attempt id the component passed to its last signInOpenRouter call.
const lastAttempt = () => {
  const calls = b.signInOpenRouter.mock.calls
  return (calls[calls.length - 1]?.[1] as string | undefined) ?? ""
}
function send(ev: Omit<OpenRouterAuth, "providerID" | "attempt">, providerID = "openrouter", attempt = lastAttempt()) {
  act(() => {
    for (const cb of listeners) cb({ ...ev, providerID, attempt })
  })
}

// A sign-in the test settles by hand.
function pending() {
  let resolve!: (p: ProviderInfo) => void
  let reject!: (e: unknown) => void
  b.signInOpenRouter.mockImplementation(
    () =>
      new Promise<ProviderInfo>((res, rej) => {
        resolve = res
        reject = rej
      }),
  )
  return { resolve: (p: ProviderInfo) => act(() => resolve(p)), reject: (e: unknown) => act(() => reject(e)) }
}

beforeEach(() => {
  listeners = []
  b.onOpenRouterAuth.mockImplementation((cb: (ev: OpenRouterAuth) => void) => {
    listeners.push(cb)
    return () => {
      listeners = listeners.filter((x) => x !== cb)
    }
  })
  b.keyStatus.mockResolvedValue({ set: false })
  b.cancelOpenRouterSignIn.mockResolvedValue(undefined)
  b.listModels.mockResolvedValue([{ id: "openai/gpt-5", label: "GPT-5", default: false }])
  b.detectLocal.mockResolvedValue([])
})
afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

describe("Sign in with OpenRouter", () => {
  it("shows each step, then hands back the signed-in provider", async () => {
    const call = pending()
    const done = vi.fn()
    render(<OpenRouterSignIn providerID="openrouter" onSignedIn={done} />)
    fireEvent.click(screen.getByRole("button", { name: "Sign in with OpenRouter" }))
    expect(b.signInOpenRouter).toHaveBeenCalledWith("openrouter", expect.stringMatching(/^or-/))
    expect(screen.queryByRole("button", { name: "Sign in with OpenRouter" })).toBeNull()
    expect(statusText()).toContain("Continue in your browser…")

    send({ status: "browser", authURL: "https://openrouter.ai/auth?x=1" })
    send({ status: "exchanging" })
    expect(statusText()).toContain("Getting the key…")
    send({ status: "verifying" })
    expect(statusText()).toContain("Checking the key…")
    // Another provider's events, and another attempt's, are not ours.
    send({ status: "exchanging" }, "other")
    send({ status: "exchanging" }, "openrouter", "or-stale")
    send({ status: "exchanging" }, "openrouter", "")
    expect(statusText()).toContain("Checking the key…")

    call.resolve(signedIn)
    await waitFor(() => expect(done).toHaveBeenCalledWith(signedIn))
    expect(screen.getByRole("button", { name: "Sign in with OpenRouter" })).toBeTruthy()
  })

  it("uses a new attempt id per try and ignores the old one's events", async () => {
    const one = pending()
    render(<OpenRouterSignIn providerID="openrouter" />)
    fireEvent.click(screen.getByRole("button", { name: "Sign in with OpenRouter" }))
    const first = lastAttempt()
    one.reject({ code: "cancelled", message: "The sign-in was cancelled." })
    pending()
    fireEvent.click(await screen.findByRole("button", { name: "Sign in with OpenRouter" }))
    expect(lastAttempt()).not.toBe(first)
    send({ status: "verifying" }, "openrouter", first)
    expect(statusText()).toContain("Continue in your browser…")
    send({ status: "exchanging" })
    expect(statusText()).toContain("Getting the key…")
  })

  it("cancels, and shows no error for a cancelled sign-in", async () => {
    const call = pending()
    render(<OpenRouterSignIn providerID="openrouter" />)
    fireEvent.click(screen.getByRole("button", { name: "Sign in with OpenRouter" }))
    fireEvent.click(screen.getByRole("button", { name: "Cancel sign-in" }))
    expect(b.cancelOpenRouterSignIn).toHaveBeenCalled()
    call.reject({ code: "cancelled", message: "The sign-in was cancelled." })
    expect(await screen.findByRole("button", { name: "Sign in with OpenRouter" })).toBeTruthy()
    expect(screen.queryByRole("alert")).toBeNull()
  })

  it("shows why a sign-in failed", async () => {
    const call = pending()
    render(<OpenRouterSignIn providerID="openrouter" />)
    fireEvent.click(screen.getByRole("button", { name: "Sign in with OpenRouter" }))
    call.reject({ code: "auth", message: "OpenRouter did not give the app a key." })
    const alert = await screen.findByRole("alert")
    expect(alert.textContent).toContain("The sign-in did not finish")
    expect(alert.textContent).toContain("OpenRouter did not give the app a key.")
  })

  it("offers the page when the browser did not open", async () => {
    pending()
    render(<OpenRouterSignIn providerID="openrouter" />)
    fireEvent.click(screen.getByRole("button", { name: "Sign in with OpenRouter" }))
    send({ status: "browser", authURL: "https://openrouter.ai/auth?x=1", browserError: "no browser" })
    const alert = await screen.findByRole("alert")
    expect(alert.textContent).toContain("The browser did not open.")
    expect((screen.getByDisplayValue("https://openrouter.ai/auth?x=1") as HTMLInputElement).readOnly).toBe(true)
  })

  it("cancels a sign-in in progress when it goes away", () => {
    pending()
    const view = render(<OpenRouterSignIn providerID="openrouter" />)
    fireEvent.click(screen.getByRole("button", { name: "Sign in with OpenRouter" }))
    view.unmount()
    expect(b.cancelOpenRouterSignIn).toHaveBeenCalled()
  })

  it("is in the provider settings' key dialog for OpenRouter only", async () => {
    b.listProviders.mockResolvedValue([openrouter, { ...openrouter, id: "custom", kind: "custom", label: "Mine" }])
    const call = pending()
    render(<ProviderSettings />)
    fireEvent.click(await screen.findByRole("button", { name: "Replace the key of Mine" }))
    expect(await screen.findByLabelText(/API key/)).toBeTruthy()
    expect(screen.queryByRole("button", { name: "Sign in with OpenRouter" })).toBeNull()
    fireEvent.keyDown(document.activeElement ?? document.body, { key: "Escape" })
    await waitFor(() => expect(screen.queryByLabelText(/API key/)).toBeNull())

    fireEvent.click(screen.getByRole("button", { name: "Replace the key of OpenRouter" }))
    fireEvent.click(await screen.findByRole("button", { name: "Sign in with OpenRouter" }))
    b.listProviders.mockResolvedValue([signedIn])
    call.resolve(signedIn)
    expect(await screen.findByText("Saved, ends in ••••c0de")).toBeTruthy()
    await waitFor(() => expect(screen.queryByRole("button", { name: "Sign in with OpenRouter" })).toBeNull())
  })

  it("is on the OpenRouter onboarding card and moves on to the model", async () => {
    b.saveProvider.mockImplementation(async (p: ProviderInfo) => ({ ...openrouter, ...p, id: "openrouter" }))
    const call = pending()
    const changed = vi.fn()
    render(
      <AssistantOnboarding
        sites={[{ name: "acme", url: "https://acme.example" }] as unknown as Site[]}
        providers={[]}
        onProvidersChanged={changed}
        onDone={() => {}}
      />,
    )
    fireEvent.click(await screen.findByRole("button", { name: /OpenRouter/ }))
    fireEvent.click(await screen.findByRole("button", { name: "Sign in with OpenRouter" }))
    expect(screen.getByLabelText(/API key/)).toBeTruthy()
    call.resolve(signedIn)
    expect(await screen.findByLabelText("Model")).toBeTruthy()
    expect(changed).toHaveBeenCalled()
    expect(b.setKey).not.toHaveBeenCalled()
  })
})
