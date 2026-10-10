import i18n, { intlLocale } from "@/i18n"

/** The sign-in method of a site in the page's language; an unknown one is shown as it is. */
export function authLabel(auth: string) {
  switch (auth) {
    case "oauth":
      return i18n.t("auth.oauth")
    case "apikey":
      return i18n.t("auth.apikey")
    case "password":
      return i18n.t("auth.password")
    default:
      return auth
  }
}

/** Up to two letters for a site's avatar: "acme-prod" -> "AP". */
export function initials(name: string) {
  const parts = name.split(/[^A-Za-z0-9]+/).filter(Boolean)
  const letters = parts.length > 1 ? parts[0][0] + parts[1][0] : name.slice(0, 2)
  return letters.toUpperCase()
}

export function hostOf(url: string) {
  try {
    return new URL(url).host
  } catch {
    return url
  }
}

export function timeAgo(iso: string) {
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return ""
  const s = Math.max(0, Math.round((Date.now() - t) / 1000))
  if (s < 60) return i18n.t("time.justNow")
  const rtf = new Intl.RelativeTimeFormat(intlLocale(), { numeric: "auto" })
  const m = Math.round(s / 60)
  if (m < 60) return rtf.format(-m, "minute")
  const h = Math.round(m / 60)
  return rtf.format(-h, "hour")
}

/**
 * The restart hint after connecting or disconnecting an app (Go's
 * internal/mcpinstall hints and removeHints, by client id), translated; Go's
 * English text when the client is new to the catalog.
 */
export function appHint(client: string, kind: "connect" | "disconnect", fallback: string) {
  // English is Go's text itself, so a reworded hint never shows stale here.
  if (!fallback || i18n.language === "en") return fallback
  // i18n keys: connectApps.hint.connect.claude-code connectApps.hint.connect.claude-desktop
  // connectApps.hint.connect.cursor connectApps.hint.connect.vscode connectApps.hint.connect.codex
  // connectApps.hint.disconnect.claude-code connectApps.hint.disconnect.claude-desktop
  // connectApps.hint.disconnect.cursor connectApps.hint.disconnect.vscode connectApps.hint.disconnect.codex
  return i18n.t(`connectApps.hint.${kind}.${client}`, { defaultValue: fallback })
}
