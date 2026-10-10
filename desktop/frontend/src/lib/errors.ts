// Errors from the Go services arrive as a Wails RuntimeError whose `cause`
// is the services.Error JSON. appError turns anything thrown into that shape,
// with the message in the page's language when Go named its key.
import i18n from "@/i18n"


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
  /** errors.<key> in the catalogs; message is its English text. */
  key?: string
  /** Values for the key's {{placeholders}}. */
  args?: Record<string, string>
}

/** A message from Go (an error, a check result) in the page's language when it names a key. */
export function localizedMessage(m: { message: string; key?: string; args?: { [k: string]: string | undefined } | null }): string {
  if (!m.key) return m.message
  return i18n.t(`errors.${m.key}`, { ...m.args, defaultValue: m.message })
}

/** The error with its message translated, when Go sent a key the catalog has. */
function localize(e: AppError): AppError {
  return e.key ? { ...e, message: localizedMessage(e) } : e
}

export function appError(err: unknown): AppError {
  const cause = (err as { cause?: unknown } | null)?.cause
  if (cause && typeof cause === "object" && "code" in cause && "message" in cause) {
    return localize(cause as AppError)
  }
  if (err && typeof err === "object" && "code" in err && "message" in err) {
    return localize(err as AppError)
  }
  const message = err instanceof Error ? err.message : String(err)
  return { code: "failed", message: message || i18n.t("errors.title.failed") }
}

/** A short title for a toast, by error kind. */
export function errorTitle(e: AppError): string {
  switch (e.code) {
    case "auth":
      return i18n.t("errors.title.auth")
    case "network":
      return i18n.t("errors.title.network")
    case "ffc_missing":
      return i18n.t("errors.title.ffcMissing")
    case "unavailable":
      return i18n.t("errors.title.unavailable")
    default:
      return i18n.t("errors.title.failed")
  }
}
