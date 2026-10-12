// Unsent work the update dialog warns about. A mounted composer reports
// whether its message box (or staged files) holds anything; unmounting
// clears it, because the draft is gone with the composer. Running
// assistant runs are asked from Go (backend.activeRuns).
const unsent = new Map<string, boolean>()

/** Reports one composer's state; returns the cleanup that forgets it. */
export function reportUnsent(id: string, has: boolean): () => void {
  unsent.set(id, has)
  return () => {
    unsent.delete(id)
  }
}

/** True when any mounted composer holds text or files not sent yet. */
export function hasUnsent(): boolean {
  return [...unsent.values()].some(Boolean)
}
