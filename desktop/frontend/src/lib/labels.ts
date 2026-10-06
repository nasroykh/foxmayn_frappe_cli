export const authLabels: Record<string, string> = {
  oauth: "Browser sign-in",
  apikey: "API key",
  password: "Password",
}

export function authLabel(auth: string) {
  return authLabels[auth] ?? auth
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
  if (s < 60) return "just now"
  const m = Math.round(s / 60)
  if (m < 60) return `${m} min ago`
  const h = Math.round(m / 60)
  return `${h} h ago`
}
