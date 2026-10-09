import { useTranslation } from "react-i18next"
import { cn } from "cn"

/** A unified diff with added and removed lines marked. */
export function DiffView({
  diff,
  label,
  headers = true,
}: {
  diff: string
  label?: string
  /** True for a file diff, whose "+++" and "---" lines are headers, not changes. */
  headers?: boolean
}) {
  const { t } = useTranslation()
  if (!diff) return <p className="text-muted-foreground pt-2 text-xs">{t("diff.none")}</p>
  return (
    <pre
      className="bg-muted mt-2 max-h-56 overflow-auto rounded-lg py-2 font-mono text-xs leading-relaxed"
      aria-label={label ?? t("diff.settingsLabel")}
    >
      {diff.split("\n").map((line, i) => {
        const added = line.startsWith("+") && !(headers && line.startsWith("+++"))
        const removed = line.startsWith("-") && !(headers && line.startsWith("---"))
        return (
          <div
            key={i}
            className={cn(
              "px-3 whitespace-pre-wrap break-all",
              added && "bg-primary/10 text-foreground",
              removed && "bg-destructive/10 text-destructive",
              !added && !removed && "text-muted-foreground",
            )}
          >
            <span className="sr-only">{added ? t("diff.added") : removed ? t("diff.removed") : ""}</span>
            {line}
          </div>
        )
      })}
    </pre>
  )
}
