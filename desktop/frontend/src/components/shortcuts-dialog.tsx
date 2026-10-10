import { useTranslation } from "react-i18next"

import { useModKey } from "@/components/app-header"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Kbd, KbdGroup } from "@/components/ui/kbd"

/** The keyboard shortcuts of the app (Ctrl+/ or the palette). */
export function ShortcutsDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const { t } = useTranslation()
  const mod = useModKey()
  // i18n keys: shortcuts.palette shortcuts.newChat shortcuts.settings shortcuts.help shortcuts.search
  // shortcuts.sidebar shortcuts.send shortcuts.newLine shortcuts.stop
  const rows: [string, string[]][] = [
    ["palette", [mod, "K"]],
    ["newChat", [mod, "N"]],
    ["settings", [mod, ","]],
    ["help", [mod, "/"]],
    ["search", [mod, "Shift", "F"]],
    ["sidebar", [mod, "B"]],
    ["send", ["Enter"]],
    ["newLine", ["Shift", "Enter"]],
    ["stop", ["Esc"]],
  ]
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t("shortcuts.title")}</DialogTitle>
          <DialogDescription>{t("shortcuts.body")}</DialogDescription>
        </DialogHeader>
        <dl className="grid grid-cols-[1fr_auto] items-center gap-x-4 gap-y-2 text-sm">
          {rows.map(([id, keys]) => (
            <div key={id} className="contents">
              <dt>{t(`shortcuts.${id}`)}</dt>
              <dd>
                <KbdGroup dir="ltr">
                  {keys.map((k) => (
                    <Kbd key={k}>{k}</Kbd>
                  ))}
                </KbdGroup>
              </dd>
            </div>
          ))}
        </dl>
      </DialogContent>
    </Dialog>
  )
}
