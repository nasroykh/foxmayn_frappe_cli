import { currentLanguage } from "@/i18n"
import type { Environment } from "@/lib/backend-types"

export const REPO = "https://github.com/nasroykh/foxmayn_frappe_cli"

/**
 * The desktop feedback issue form (.github/ISSUE_TEMPLATE/desktop-feedback.yml),
 * with the app version, the OS and the language filled in. Nothing else about
 * the person or their sites goes into the link.
 */
export function feedbackURL(env: Environment | null): string {
  const q = new URLSearchParams({ template: "desktop-feedback.yml", lang: currentLanguage() })
  if (env) {
    q.set("version", env.appVersion)
    q.set("os", env.os === "darwin" ? "macOS" : env.os === "windows" ? "Windows" : env.os)
  }
  return `${REPO}/issues/new?${q.toString()}`
}
