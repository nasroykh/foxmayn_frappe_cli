// Token and cost totals, as the chat shows them. The Go side decides what a
// cost is (provider price, price table, or unknown); this only words it.
import type { ChatUsage, UsageTotals } from "@/lib/backend-types"

export const noUsage: UsageTotals = {
  input: 0,
  output: 0,
  cached: 0,
  cacheWrite: 0,
  costUSD: 0,
  hasCost: false,
  unknown: false,
}

/**
 * none: tokens only (a local model, or nothing used yet).
 * unknown: some call has no known cost and none has one.
 * atLeast: some call is unknown, the others add up to usd.
 * exact: every call has a cost.
 */
export type CostState =
  | { kind: "none" }
  | { kind: "unknown" }
  | { kind: "atLeast"; usd: number }
  | { kind: "exact"; usd: number }

export function costState(u: UsageTotals): CostState {
  if (u.unknown) return u.hasCost ? { kind: "atLeast", usd: u.costUSD } : { kind: "unknown" }
  return u.hasCost ? { kind: "exact", usd: u.costUSD } : { kind: "none" }
}

/** True when a call was counted at all. */
export function hasUsage(u: UsageTotals): boolean {
  return u.input + u.output + u.cached > 0
}

/** Adds one chat:usage event (a live turn) to a running total. */
export function addEvent(u: UsageTotals, ev: ChatUsage): UsageTotals {
  const next = {
    ...u,
    input: u.input + ev.input,
    output: u.output + ev.output,
    cached: u.cached + ev.cached,
    cacheWrite: u.cacheWrite + ev.cacheWrite,
  }
  if (ev.cost != null) return { ...next, costUSD: u.costUSD + ev.cost, hasCost: true }
  // Only a call the app could not price is unknown. A local model has tokens
  // and no cost, and a row from before 0.3.0 (costSource "") is tokens only.
  return ev.costSource === "unknown" ? { ...next, unknown: true } : next
}

/** The sum of two totals. */
export function mergeTotals(a: UsageTotals, b: UsageTotals): UsageTotals {
  return {
    input: a.input + b.input,
    output: a.output + b.output,
    cached: a.cached + b.cached,
    cacheWrite: a.cacheWrite + b.cacheWrite,
    costUSD: a.costUSD + b.costUSD,
    hasCost: a.hasCost || b.hasCost,
    unknown: a.unknown || b.unknown,
  }
}

/**
 * Dollars: two decimals from $1 up, four below it so a small price is not
 * $0.00, and "<$0.0001" for a price that would round to nothing. The format
 * is chosen after rounding, so $0.99999 reads $1.00.
 */
export function formatUSD(usd: number, locale?: string): string {
  const money = (v: number, digits: number) =>
    new Intl.NumberFormat(locale, { style: "currency", currency: "USD", minimumFractionDigits: digits, maximumFractionDigits: digits }).format(v)
  if (usd > 0 && usd < 0.00005) return "<" + money(0.0001, 4)
  const rounded4 = Math.round(usd * 1e4) / 1e4
  return money(usd, rounded4 < 1 ? 4 : 2)
}
