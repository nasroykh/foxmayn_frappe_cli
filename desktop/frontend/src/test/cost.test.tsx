import { cleanup, render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it } from "vitest"

import "@/i18n"
import { UsageLine } from "@/components/chat/usage-line"
import type { UsageTotals } from "@/lib/backend-types"
import { addEvent, costState, formatUSD, hasUsage, noUsage } from "@/lib/cost"

afterEach(cleanup)

const u = (over: Partial<UsageTotals>): UsageTotals => ({ ...noUsage, input: 1840, output: 212, cached: 1500, ...over })

describe("cost state", () => {
  it("tells exact, at least, unknown and tokens only apart", () => {
    expect(costState(u({ hasCost: true, costUSD: 0.5 }))).toEqual({ kind: "exact", usd: 0.5 })
    expect(costState(u({ hasCost: true, costUSD: 0.5, unknown: true }))).toEqual({ kind: "atLeast", usd: 0.5 })
    expect(costState(u({ unknown: true }))).toEqual({ kind: "unknown" })
    expect(costState(u({}))).toEqual({ kind: "none" })
  })

  it("counts a turn without a cost as unknown, except a local one", () => {
    const ev = { convID: "c", runID: "r", turn: 1, input: 10, output: 5, cached: 0, cacheWrite: 0 }
    expect(addEvent(noUsage, { ...ev, cost: 0.02, costSource: "table" })).toMatchObject({ hasCost: true, unknown: false, costUSD: 0.02 })
    expect(addEvent(noUsage, { ...ev, cost: null, costSource: "" })).toMatchObject({ hasCost: false, unknown: true })
    expect(addEvent(noUsage, { ...ev, cost: null, costSource: "local" })).toMatchObject({ hasCost: false, unknown: false, input: 10 })
  })

  it("shows a small price with more decimals", () => {
    expect(formatUSD(0.0123, "en")).toBe("$0.0123")
    expect(formatUSD(1.5, "en")).toBe("$1.50")
    expect(hasUsage(noUsage)).toBe(false)
  })
})

describe("usage line", () => {
  it("says cost unknown", () => {
    render(<UsageLine usage={u({ unknown: true })} />)
    expect(screen.getByText(/1,840 in · 212 out · 1,500 cached · cost unknown/)).toBeTruthy()
  })

  it("says at least when some turn is unknown", () => {
    render(<UsageLine usage={u({ hasCost: true, costUSD: 0.0456, unknown: true })} />)
    expect(screen.getByText(/at least \$0\.0456/)).toBeTruthy()
  })

  it("shows the price when every turn has one", () => {
    render(<UsageLine usage={u({ hasCost: true, costUSD: 1.2 })} />)
    const line = screen.getByText(/\$1\.20/)
    expect(line.textContent).not.toMatch(/unknown|at least/)
  })

  it("shows tokens only for a local model, and nothing when nothing was used", () => {
    const { container, rerender } = render(<UsageLine usage={u({})} />)
    expect(container.textContent).toBe("1,840 in · 212 out · 1,500 cached")
    rerender(<UsageLine usage={noUsage} />)
    expect(container.textContent).toBe("")
  })
})
