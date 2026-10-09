import { cn } from "cn"

/** A unified diff with added and removed lines marked. */
export function DiffView({ diff, label = "Changes to the settings file" }: { diff: string; label?: string }) {
  if (!diff) return <p className="text-muted-foreground pt-2 text-xs">No changes.</p>
  return (
    <pre
      className="bg-muted mt-2 max-h-56 overflow-auto rounded-lg py-2 font-mono text-xs leading-relaxed"
      aria-label={label}
    >
      {diff.split("\n").map((line, i) => {
        const added = line.startsWith("+") && !line.startsWith("+++")
        const removed = line.startsWith("-") && !line.startsWith("---")
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
            <span className="sr-only">{added ? "Added: " : removed ? "Removed: " : ""}</span>
            {line}
          </div>
        )
      })}
    </pre>
  )
}
