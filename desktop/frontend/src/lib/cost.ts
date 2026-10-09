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
  // A local model has tokens and no cost; any other call without one is unknown.
  return ev.costSource === "local" ? next : { ...next, unknown: true }
}

/** Dollars: two decimals from $1 up, four below it so a small price is not 0.00. */
export function formatUSD(usd: number, locale?: string): string {
  const small = usd < 1
  return new Intl.NumberFormat(locale, {
    style: "currency",
    currency: "USD",
    minimumFractionDigits: small ? 4 : 2,
    maximumFractionDigits: small ? 4 : 2,
  }).format(usd)
}
