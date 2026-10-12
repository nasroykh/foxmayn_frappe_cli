import * as React from "react"
import { useTranslation } from "react-i18next"

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogMedia,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Spinner } from "@/components/ui/spinner"

/**
 * The app's own confirmation (Go no longer asks natively). Cancel is the
 * initial focus, so Enter never confirms by accident; the action is
 * destructive-styled because everything asked here opens access or deletes
 * something. Site names go in the text as <bdi>.
 */
export function ConfirmDialog({
  open,
  onOpenChange,
  icon,
  title,
  description,
  children,
  confirmLabel,
  onConfirm,
  busy = false,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  icon?: React.ReactNode
  title: React.ReactNode
  description: React.ReactNode
  /** Extra content under the description (a list of what opens, for example). */
  children?: React.ReactNode
  confirmLabel: string
  onConfirm: () => void
  busy?: boolean
}) {
  const { t } = useTranslation()
  const cancelRef = React.useRef<HTMLButtonElement>(null)
  return (
    <AlertDialog open={open} onOpenChange={(o) => !(busy && !o) && onOpenChange(o)}>
      <AlertDialogContent initialFocus={cancelRef}>
        <AlertDialogHeader>
          {icon && <AlertDialogMedia>{icon}</AlertDialogMedia>}
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
        </AlertDialogHeader>
        {children}
        <AlertDialogFooter>
          <AlertDialogCancel ref={cancelRef} disabled={busy}>
            {t("common.cancel")}
          </AlertDialogCancel>
          <AlertDialogAction variant="destructive" className="text-red-700 dark:text-red-300" onClick={onConfirm} disabled={busy}>
            {busy && <Spinner data-icon="inline-start" />}
            {confirmLabel}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
