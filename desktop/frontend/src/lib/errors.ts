// Errors from the Go services arrive as a Wails RuntimeError whose `cause`
// is the services.Error JSON. appError turns anything thrown into that shape.

export type ErrorCode =
  | "invalid"
  | "exists"
  | "not_found"
  | "auth"
  | "network"
  | "cancelled"
  | "no_registration"
  | "ffc_missing"
  | "unavailable"
  | "failed"

export interface AppError {
  code: ErrorCode
  message: string
  detail?: string
  field?: string
  unsupported?: boolean
  redirectURI?: string
}

export function appError(err: unknown): AppError {
  const cause = (err as { cause?: unknown } | null)?.cause
  if (cause && typeof cause === "object" && "code" in cause && "message" in cause) {
    return cause as AppError
  }
  if (err && typeof err === "object" && "code" in err && "message" in err) {
    return err as AppError
  }
  const message = err instanceof Error ? err.message : String(err)
  return { code: "failed", message: message || "Something went wrong." }
}

/** A short title for a toast, by error kind. */
export function errorTitle(e: AppError): string {
  switch (e.code) {
    case "auth":
      return "The site refused the sign-in"
    case "network":
      return "Could not reach the site"
    case "ffc_missing":
      return "The ffc helper is not installed"
    case "unavailable":
      return "Not available"
    default:
      return "Something went wrong"
  }
}
