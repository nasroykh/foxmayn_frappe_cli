import * as React from "react"

export const LIST_WIDTH = { min: 220, max: 420, initial: 288, step: 16 }
const KEY = "ffd-conversation-list-width"

const clamp = (w: number) => Math.min(LIST_WIDTH.max, Math.max(LIST_WIDTH.min, Math.round(w)))

function stored(): number {
  try {
    const v = Number(localStorage.getItem(KEY))
    return v ? clamp(v) : LIST_WIDTH.initial
  } catch {
    return LIST_WIDTH.initial
  }
}

/**
 * The conversation list's width in pixels, kept for this computer (a
 * per-viewer convenience: a blocked storage just means the default).
 */
export function useListWidth() {
  const [width, setWidthState] = React.useState(stored)
  // keep false while dragging: the width is saved once, when the drag ends.
  const setWidth = React.useCallback((w: number, keep = true) => {
    const v = clamp(w)
    setWidthState(v)
    if (!keep) return
    try {
      localStorage.setItem(KEY, String(v))
    } catch {
      // Not kept; the width still applies until the app closes.
    }
  }, [])
  return [width, setWidth] as const
}
