/**
 * Open popups that own the keyboard: dialogs, alert dialogs and sheets. Left
 * out: toasts, which Base UI also marks role="dialog" (not modal), and a
 * popup playing its exit animation (data-closed, about 200 ms), so a visible
 * toast or a dialog that just closed never holds back shortcuts or Esc.
 */
export const MODAL =
  '[role="dialog"]:not([data-slot="toast"]):not([data-closed]), [role="alertdialog"]:not([data-closed])'

export function modalOpen(): boolean {
  return !!document.querySelector(MODAL)
}
