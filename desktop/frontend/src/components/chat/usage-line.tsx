import { useTranslation } from "react-i18next"
import { cn } from "cn"

import { intlLocale } from "@/i18n"
import type { UsageTotals } from "@/lib/backend-types"
import { costState, formatUSD, hasUsage } from "@/lib/cost"

/** The cost words: "$0.0123", "at least $0.0123", "cost unknown", or "" for tokens only. */
export function useCostText() {
  const { t } = useTranslation()
  return (u: UsageTotals) => {
    const c = costState(u)
    switch (c.kind) {
      case "exact":
        return formatUSD(c.usd, intlLocale())
      case "atLeast":
        return t("chat.usage.atLeast", { amount: formatUSD(c.usd, intlLocale()) })
      case "unknown":
        return t("chat.usage.unknown")
      default:
        return ""
    }
  }
}

/** One line under an answer: tokens, then the cost. Renders nothing when no call was counted. */
export function UsageLine({ usage, className }: { usage: UsageTotals; className?: string }) {
  const { t } = useTranslation()
  const costText = useCostText()
  if (!hasUsage(usage)) return null
  const n = (v: number) => v.toLocaleString(intlLocale())
  const parts = [t("chat.usage.tokens", { input: n(usage.input), output: n(usage.output) })]
  if (usage.cached > 0) parts.push(t("chat.usage.cached", { cached: n(usage.cached) }))
  const cost = costText(usage)
  if (cost) parts.push(cost)
  const kind = costState(usage).kind
  return (
    <p
      className={cn("text-muted-foreground text-xs", className)}
      data-cost-kind={kind}
      title={kind === "unknown" || kind === "atLeast" ? t("chat.usage.unknownHelp") : undefined}
    >
      {parts.join(" · ")}
    </p>
  )
}
