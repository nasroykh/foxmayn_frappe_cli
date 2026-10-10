import {
  IconAlertTriangle,
  IconCheck,
  IconFileText,
  IconPaperclip,
  IconPencil,
  IconFileTypePdf,
  IconPhoto,
  IconPlayerPlay,
  IconPlayerStop,
  IconSend,
  IconX,
} from "@tabler/icons-react"
import * as React from "react"
import { useTranslation } from "react-i18next"
import { cn } from "cn"

import { ApprovalCard } from "@/components/chat/approval-card"
import { ChatMarkdown } from "@/components/chat/markdown"
import { ToolRow } from "@/components/chat/tool-row"
import { UsageLine, useCostText } from "@/components/chat/usage-line"
import { Alert, AlertAction, AlertDescription, AlertTitle } from "@/components/ui/alert"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Textarea } from "@/components/ui/textarea"
import { toast } from "@/components/ui/toast"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { intlLocale } from "@/i18n"
import { backend } from "@/lib/backend"
import type {
  ChatMessage,
  Conversation,
  ConversationMode,
  ProviderInfo,
  RunUsage,
  StagedAttachment,
  ToolApproval,
  ToolStatus,
  UsageTotals,
} from "@/lib/backend-types"
import { hasUsage, mergeTotals, noUsage } from "@/lib/cost"
import { appError, errorTitle, localizedMessage, type AppError } from "@/lib/errors"
import { MODAL } from "@/lib/modal"
import { chatReducer, initialState } from "@/screens/assistant/chat-reducer"
import { MessageFooter } from "@/screens/assistant/message-footer"
import { ProfilePicker } from "@/screens/assistant/profile-picker"

/** A message from an imported file (its id says so): never edited or run again. */
function isImported(id: string) {
  return id.startsWith("imp_")
}

function notify(err: unknown) {
  const e = appError(err)
  toast.add({ title: errorTitle(e), description: e.message, type: "error" })
}

const MAX_PASTED_BYTES = 5 << 20

/** Reads a pasted file as a data URL. */
function readDataURL(file: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const r = new FileReader()
    r.onload = () => resolve(String(r.result))
    r.onerror = () => reject(r.error ?? new Error("read failed"))
    r.readAsDataURL(file)
  })
}

const OVERLAYS = `${MODAL},[role="menu"],[role="listbox"]`

/** Esc belongs to an open dialog, menu or list first. */
function escapeIsTaken(e: KeyboardEvent) {
  return e.defaultPrevented || !!document.querySelector(OVERLAYS) || (e.target instanceof Element && !!e.target.closest(OVERLAYS))
}

/** One open conversation: header, messages, live run and composer. */
export function ChatPane({
  conv,
  providers,
  onChanged,
}: {
  conv: Conversation
  providers: ProviderInfo[]
  /** The conversation list may be stale (title, order, mode): reload it. */
  onChanged: () => void
}) {
  const { t } = useTranslation()
  const convID = conv.id
  const [state, dispatch] = React.useReducer(chatReducer, convID, initialState)
  const [messages, setMessages] = React.useState<ChatMessage[] | null>(null)
  const [loadError, setLoadError] = React.useState<AppError | null>(null)
  const [mode, setMode] = React.useState<ConversationMode>(conv.mode === "ask" ? "ask" : "read")
  const [answering, setAnswering] = React.useState<ReadonlySet<string>>(new Set())
  const [draft, setDraft] = React.useState("")
  const [staged, setStaged] = React.useState<StagedAttachment[]>([])
  const [attaching, setAttaching] = React.useState(false)
  const [runUsage, setRunUsage] = React.useState<RunUsage[]>([])
  const [total, setTotal] = React.useState<UsageTotals>(noUsage)
  const [renaming, setRenaming] = React.useState<string | null>(null)
  // A message action waiting for the user's yes (edit or delete).
  const [confirming, setConfirming] = React.useState<{ kind: "edit" | "delete"; msg: ChatMessage } | null>(null)
  const [acting, setActing] = React.useState(false)
  const costText = useCostText()
  const stateRef = React.useRef(state)
  stateRef.current = state
  const convRef = React.useRef(convID)
  convRef.current = convID
  const [announce, setAnnounce] = React.useState("")
  const reloadAfterStart = React.useRef(false)
  const endRef = React.useRef<HTMLDivElement>(null)
  const inputRef = React.useRef<HTMLTextAreaElement>(null)
  const onChangedRef = React.useRef(onChanged)
  onChangedRef.current = onChanged

  const active = state.phase === "running" || state.phase === "starting"

  // The store is the truth: (re)load the conversation, then ask for open cards.
  const load = React.useCallback(async () => {
    const id = convRef.current
    try {
      const detail = await backend.getConversation(id)
      if (convRef.current !== id) return
      setMessages(detail.messages)
      setRunUsage(detail.runUsage)
      setTotal(detail.total)
      setMode(detail.conversation.mode === "ask" ? "ask" : "read")
      setLoadError(null)
      dispatch({ type: "loaded", detail })
      if (detail.activeRunID) {
        const list = await backend.pendingApprovals(id)
        if (convRef.current === id) dispatch({ type: "approvals", list })
      }
    } catch (err) {
      if (convRef.current === id) setLoadError(appError(err))
    }
  }, [])

  // Another conversation: a fresh state and a load.
  React.useEffect(() => {
    dispatch({ type: "select", convID })
    setMessages(null)
    setRunUsage([])
    setTotal(noUsage)
    setRenaming(null)
    setLoadError(null)
    setDraft("")
    setStaged([])
    setMode(conv.mode === "ask" ? "ask" : "read")
    void load()
    // Files staged before (they stay in the store until sent or removed).
    backend
      .listAttachments(convID)
      .then((list) => {
        if (convRef.current === convID) setStaged(list)
      })
      .catch(() => {})
    // conv.mode only seeds the first paint; load() sets the real one.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [convID, load])

  // The chat events. Cleaned up on unmount; the reducer drops other runs' events.
  React.useEffect(() => {
    const offs = [
      // Files dropped on this composer, read by Go.
      backend.onChatAttachments((ev) => {
        if (ev.convID !== convRef.current) return
        setStaged((s) => [...s, ...ev.attachments])
        for (const msg of ev.errors) attachErrorRef.current(msg)
      }),
      backend.onChatDelta((ev) => dispatch({ type: "delta", convID: ev.convID, runID: ev.runID, text: ev.text })),
      backend.onChatTool((ev) => dispatch({ type: "tool", ev })),
      backend.onChatUsage((ev) => dispatch({ type: "usage", ev })),
      backend.onChatTitle((ev) => {
        if (ev.convID !== convRef.current) return
        onChangedRef.current()
        // The title call is part of the total. A reload during a run would reset its live view;
        // the reload at the run's end (chat:done) then picks the title call up.
        if (stateRef.current.phase === "idle") void load()
      }),
      backend.onChatApproval((ev) => dispatch({ type: "approval", ev })),
      backend.onChatApprovalClosed((ev) =>
        dispatch({ type: "approvalClosed", convID: ev.convID, runID: ev.runID, approvalID: ev.approvalID }),
      ),
      backend.onChatError((ev) => dispatch({ type: "error", ev })),
      backend.onChatDone((ev) => {
        // Events carry their conversation: those of another one are not ours.
        if (ev.convID !== convRef.current) return
        dispatch({ type: "done", ev })
        if (stateRef.current.phase === "starting") reloadAfterStart.current = true
        else void load()
        onChangedRef.current()
      }),
    ]
    return () => offs.forEach((off) => off())
  }, [load])

  // Esc stops the run, unless a dialog is open (Esc closes that first).
  React.useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape" || escapeIsTaken(e)) return
      const s = stateRef.current
      if (s.phase === "running" && s.runID) void stop(s.runID)
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [])

  React.useEffect(() => {
    endRef.current?.scrollIntoView?.({ block: "end" })
  }, [messages, state.text, state.tools, state.approvals, state.pending, state.error, state.phase])

  // Forget answered cards once they are gone.
  React.useEffect(() => {
    setAnswering((s) => {
      const open = new Set(state.approvals.map((c) => c.approvalID))
      const next = new Set([...s].filter((id) => open.has(id)))
      return next.size === s.size ? s : next
    })
  }, [state.approvals])

  // What a screen reader hears, apart from the streaming text itself.
  const wasActive = React.useRef(false)
  React.useEffect(() => {
    if (state.approvals.length > 0) setAnnounce(t("chat.announce.approval"))
    else if (active) setAnnounce("")
    else if (wasActive.current && !state.error) setAnnounce(t("chat.announce.finished"))
    wasActive.current = active
  }, [active, state.approvals.length, state.error, t])

  // Back to typing once a run is over.
  React.useEffect(() => {
    if (!active && document.activeElement === document.body) inputRef.current?.focus()
  }, [active])

  async function stop(runID: string) {
    try {
      await backend.cancelRun(runID)
    } catch (err) {
      notify(err)
    }
  }

  function attachError(message: string) {
    toast.add({ title: t("chat.composer.attachFailed"), description: message, type: "error" })
  }
  const attachErrorRef = React.useRef(attachError)
  attachErrorRef.current = attachError

  async function pickFiles() {
    const id = convID
    setAttaching(true)
    try {
      const res = await backend.addAttachment(id)
      if (convRef.current === id) setStaged((s) => [...s, ...res.attachments])
      for (const msg of res.errors) attachError(msg)
    } catch (err) {
      notify(err)
    } finally {
      setAttaching(false)
    }
  }

  async function pasteImage(file: File) {
    const id = convID
    if (file.size > MAX_PASTED_BYTES) {
      attachError(t("chat.composer.pasteTooBig"))
      return
    }
    try {
      const a = await backend.addPastedImage(id, await readDataURL(file))
      if (convRef.current === id) setStaged((s) => [...s, a])
    } catch (err) {
      attachError(appError(err).message)
    }
  }

  function onPaste(e: React.ClipboardEvent<HTMLTextAreaElement>) {
    const images = Array.from(e.clipboardData?.files ?? []).filter((f) => f.type.startsWith("image/"))
    if (images.length === 0) return
    e.preventDefault()
    for (const f of images) void pasteImage(f)
  }

  async function unstage(a: StagedAttachment) {
    setStaged((s) => s.filter((x) => x.id !== a.id))
    try {
      await backend.removeAttachment(convID, a.id)
    } catch (err) {
      // Gone already (sent or removed elsewhere): nothing to put back.
      if (appError(err).code !== "not_found") {
        notify(err)
        setStaged((s) => [...s, a])
      }
    }
  }

  async function send() {
    const text = draft.trim()
    const files = staged
    if ((!text && files.length === 0) || active) return
    setDraft("")
    setStaged([])
    reloadAfterStart.current = false
    dispatch({ type: "sending", text: text || files.map((f) => f.name).join(", ") })
    try {
      const runID = await backend.sendMessage(
        convID,
        text,
        files.map((f) => f.id),
      )
      dispatch({ type: "started", runID })
      // The run ended before its id came back: the store has the rest.
      if (reloadAfterStart.current) {
        reloadAfterStart.current = false
        void load()
      }
    } catch (err) {
      const e = appError(err)
      setDraft(text)
      setStaged(files)
      dispatch({ type: "sendFailed", error: { code: e.code, message: e.message, detail: e.detail, key: e.key, args: e.args } })
    }
  }

  // Edit: the prompt and what follows leave the conversation; its text and
  // files go back to the composer.
  async function editMessage(m: ChatMessage) {
    setActing(true)
    try {
      const r = await backend.rewind(convID, m.id)
      // Text not sent yet stays, after the message being edited.
      setDraft((d) => (d.trim() ? `${r.text}\n\n${d}` : r.text))
      setStaged(r.attachments)
      await load()
      onChangedRef.current()
      requestAnimationFrame(() => {
        const el = inputRef.current
        if (!el) return
        el.focus()
        el.setSelectionRange(el.value.length, el.value.length)
      })
    } catch (err) {
      notify(err)
    } finally {
      setActing(false)
      setConfirming(null)
    }
  }

  async function deleteMessage(m: ChatMessage) {
    setActing(true)
    try {
      await backend.deleteExchange(convID, m.id)
      await load()
      onChangedRef.current()
    } catch (err) {
      notify(err)
    } finally {
      setActing(false)
      setConfirming(null)
    }
  }

  async function retry() {
    if (active) return
    reloadAfterStart.current = false
    dispatch({ type: "sending", text: "" })
    // The last answer goes from the screen; the run brings the new one.
    setMessages((ms) => {
      if (!ms) return ms
      const last = ms.map((x) => x.role).lastIndexOf("user")
      return last < 0 ? ms : ms.slice(0, last + 1)
    })
    try {
      const runID = await backend.retry(convID)
      dispatch({ type: "started", runID })
      if (reloadAfterStart.current) {
        reloadAfterStart.current = false
        void load()
      }
    } catch (err) {
      const e = appError(err)
      void load()
      dispatch({ type: "sendFailed", error: { code: e.code, message: e.message, detail: e.detail, key: e.key, args: e.args } })
    }
  }

  async function answer(approvalID: string, approve: boolean) {
    setAnswering((s) => new Set(s).add(approvalID))
    try {
      await backend.answerApproval(convID, approvalID, approve)
    } catch (err) {
      notify(err)
      // A card that is gone on the other side (run ended) must not stay.
      if (appError(err).code === "not_found") dispatch({ type: "approvalClosed", convID, runID: state.runID, approvalID })
      setAnswering((s) => {
        const next = new Set(s)
        next.delete(approvalID)
        return next
      })
    }
    // On success the card stays busy until chat:approval-closed (or done) removes it.
  }

  async function resume() {
    const runID = state.runID
    try {
      await backend.continueRun(runID)
      dispatch({ type: "resumed" })
    } catch (err) {
      notify(err)
    }
  }

  async function changeMode(next: ConversationMode) {
    const before = mode
    setMode(next)
    try {
      await backend.setConversationMode(convID, next)
      onChanged()
    } catch (err) {
      setMode(before)
      notify(err)
    }
  }

  async function saveName() {
    const name = (renaming ?? "").trim()
    if (!name) {
      setRenaming(null)
      return
    }
    // Always sent, even when unchanged: confirming a title locks it as the user's own.
    try {
      await backend.renameConversation(convID, name)
      setRenaming(null)
      onChanged()
    } catch (err) {
      notify(err)
    }
  }

  const provider = providers.find((p) => p.id === conv.providerID)
  const usageOf = new Map(runUsage.map((u) => [u.msgID, u.usage]))
  // Try again sits on the last message: the last answer, or a last prompt
  // left without one (a failed or stopped run), when a prompt of the user's
  // own (not imported) leads to it.
  const lastPrompt = messages ? messages.map((x) => x.role).lastIndexOf("user") : -1
  const lastAnswer =
    messages && lastPrompt >= 0 && !isImported(messages[lastPrompt].id) ? messages.length - 1 : -1
  // The stored total plus the model turns of the run in progress (a reload resets those).
  const shownTotal = active ? mergeTotals(total, state.usage) : total

  return (
    <section aria-label={conv.title || t("chat.title")} className="flex h-full min-h-0 min-w-0 flex-1 flex-col">
      <header className="flex flex-wrap items-center gap-x-4 gap-y-2 border-b px-4 py-2">
        <div className="flex min-w-0 flex-1 flex-col">
          {renaming === null ? (
            <div className="flex min-w-0 items-center gap-1">
              <h2 className="truncate text-sm font-semibold">{conv.title || t("chat.list.untitled")}</h2>
              <Button
                type="button"
                variant="ghost"
                size="icon-xs"
                aria-label={t("chat.header.renameLabel")}
                title={t("chat.header.rename")}
                onClick={() => setRenaming(conv.title)}
              >
                <IconPencil />
              </Button>
            </div>
          ) : (
            <form
              className="flex items-center gap-1"
              onSubmit={(e) => {
                e.preventDefault()
                void saveName()
              }}
            >
              <Input
                autoFocus
                value={renaming}
                maxLength={80}
                aria-label={t("chat.header.renameField")}
                onChange={(e) => setRenaming(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Escape") {
                    // Esc closes the editor, not the run.
                    e.preventDefault()
                    setRenaming(null)
                  }
                }}
                className="h-7 text-sm"
              />
              <Button type="submit" variant="ghost" size="icon-xs" aria-label={t("chat.header.renameSave")}>
                <IconCheck />
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="icon-xs"
                aria-label={t("chat.header.renameCancel")}
                onClick={() => setRenaming(null)}
              >
                <IconX />
              </Button>
            </form>
          )}
          <p className="text-muted-foreground truncate text-xs">
            {t("chat.header.site", { site: conv.site })} · {provider?.label || conv.providerID}
            {conv.model ? ` · ${conv.model}` : ""}
            {hasUsage(shownTotal) && (
              <span title={t("chat.header.totalHelp")} data-testid="usage-total">
                {" · "}
                {t("chat.header.total", {
                  usage: [
                    t("chat.usage.tokens", {
                      input: shownTotal.input.toLocaleString(intlLocale()),
                      output: shownTotal.output.toLocaleString(intlLocale()),
                    }),
                    costText(shownTotal),
                  ]
                    .filter(Boolean)
                    .join(" · "),
                })}
              </span>
            )}
          </p>
        </div>
        <ProfilePicker conv={conv} disabled={active} onChanged={() => onChanged()} />
        <Tooltip>
          <TooltipTrigger render={<span tabIndex={active ? 0 : -1} className="inline-flex" />}>
            <ToggleGroup
              variant="outline"
              size="sm"
              value={[mode]}
              disabled={active}
              onValueChange={(v: string[]) => v[0] && v[0] !== mode && void changeMode(v[0] as ConversationMode)}
              aria-label={t("chat.mode.label")}
            >
              <ToggleGroupItem value="read">{t("chat.mode.read")}</ToggleGroupItem>
              <ToggleGroupItem value="ask">{t("chat.mode.ask")}</ToggleGroupItem>
            </ToggleGroup>
          </TooltipTrigger>
          <TooltipContent className="max-w-64">
            {active ? t("chat.mode.locked") : mode === "read" ? t("chat.mode.readHelp") : t("chat.mode.askHelp")}
          </TooltipContent>
        </Tooltip>
      </header>

      <div className="min-h-0 flex-1 overflow-y-auto px-4 py-4">
        <div className="mx-auto flex max-w-3xl flex-col gap-4">
          {loadError && (
            <Alert variant="destructive" role="alert">
              <IconAlertTriangle />
              <AlertTitle>{t("chat.loadFailed")}</AlertTitle>
              <AlertDescription>{loadError.message}</AlertDescription>
              <AlertAction>
                <Button size="sm" variant="outline" onClick={() => void load()}>
                  {t("common.retry")}
                </Button>
              </AlertAction>
            </Alert>
          )}
          {messages === null && !loadError && <Skeleton className="h-16 w-2/3" />}
          {messages?.length === 0 && !state.pending && (
            <StarterPrompts
              profileID={conv.profileID}
              disabled={active}
              onPick={(text) => {
                setDraft(text)
                // After the render that puts the text in: caret at its end.
                requestAnimationFrame(() => {
                  const el = inputRef.current
                  if (!el) return
                  el.focus()
                  el.setSelectionRange(text.length, text.length)
                })
              }}
            />
          )}
          {messages?.map((m, i) => (
            <div key={m.id} className="group/msg flex flex-col gap-1">
              <MessageView m={m} />
              <MessageFooter
                m={m}
                usage={usageOf.get(m.id)}
                busy={active || acting}
                canRetry={i === lastAnswer && !active}
                canEdit={!isImported(m.id)}
                onEdit={() => {
                  // Nothing after it: no answer is lost, so no question.
                  if (i === messages.length - 1) void editMessage(m)
                  else setConfirming({ kind: "edit", msg: m })
                }}
                onDelete={() => setConfirming({ kind: "delete", msg: m })}
                onRetry={() => void retry()}
              />
            </div>
          ))}
          {state.pending && <UserBubble text={state.pending} />}

          {state.tools.length > 0 && (
            <div className="flex flex-col gap-1.5">
              {state.tools.map((tc) => (
                <ToolRow key={tc.callID} tool={tc.tool} site={tc.site} status={tc.status} summary={tc.summary} />
              ))}
            </div>
          )}

          {/* The streaming reply: announced politely as it grows. */}
          <div aria-busy={active}>
            {state.text && <ChatMarkdown text={state.text} />}
            {active && !state.text && state.approvals.length === 0 && (
              <p className="text-muted-foreground text-sm" role="status">
                {t("chat.thinking")}
              </p>
            )}
          </div>

          {active && <UsageLine usage={state.usage} />}

          {state.approvals.map((card) => (
            <ApprovalCard
              key={card.approvalID}
              card={card}
              busy={answering.has(card.approvalID)}
              onAnswer={(approve) => void answer(card.approvalID, approve)}
            />
          ))}

          {state.error && (
            <Alert variant="destructive" role="alert">
              <IconAlertTriangle />
              <AlertTitle>{t("chat.error.title")}</AlertTitle>
              <AlertDescription>
                <p>{localizedMessage(state.error)}</p>
              </AlertDescription>
            </Alert>
          )}

          {state.phase === "paused" && (
            <Alert>
              <IconPlayerPlay />
              <AlertTitle>{t("chat.paused.title")}</AlertTitle>
              <AlertDescription>{t("chat.paused.body")}</AlertDescription>
              <AlertAction>
                <Button size="sm" onClick={() => void resume()}>
                  {t("chat.paused.continue")}
                </Button>
              </AlertAction>
            </Alert>
          )}
          <p role="status" aria-live="polite" className="sr-only">
            {announce}
          </p>
          <div ref={endRef} />
        </div>
      </div>

      <form
        className="[&.file-drop-target-active]:bg-muted border-t p-3"
        data-file-drop-target
        data-conv-id={convID}
        onSubmit={(e) => {
          e.preventDefault()
          void send()
        }}
      >
        {staged.length > 0 && (
          <div className="mx-auto mb-2 max-w-3xl">
            <AttachmentChips items={staged} label={t("chat.composer.staged")} onRemove={(a) => void unstage(a)} />
          </div>
        )}
        <div className="mx-auto flex max-w-3xl items-end gap-2">
          <Button
            type="button"
            variant="outline"
            size="icon"
            disabled={active || attaching}
            onClick={() => void pickFiles()}
            aria-label={t("chat.composer.attachLabel")}
            title={t("chat.composer.attachLabel")}
          >
            <IconPaperclip />
          </Button>
          <label htmlFor="chat-input" className="sr-only">
            {t("chat.composer.label")}
          </label>
          <Textarea
            id="chat-input"
            ref={inputRef}
            rows={2}
            value={draft}
            disabled={active}
            placeholder={t("chat.composer.placeholder")}
            onChange={(e) => setDraft(e.target.value)}
            onPaste={onPaste}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
                e.preventDefault()
                void send()
              }
            }}
            className="max-h-40 min-h-12 resize-none"
          />
          {active ? (
            <Button
              type="button"
              variant="outline"
              disabled={!state.runID}
              onClick={() => void stop(state.runID)}
              aria-label={t("chat.composer.stopLabel")}
            >
              <IconPlayerStop data-icon="inline-start" />
              {t("chat.composer.stop")}
            </Button>
          ) : (
            <Button type="submit" disabled={!draft.trim() && staged.length === 0} aria-label={t("chat.composer.sendLabel")}>
              <IconSend data-icon="inline-start" />
              {t("chat.composer.send")}
            </Button>
          )}
        </div>
        <p className="text-muted-foreground mx-auto mt-1 max-w-3xl text-xs">
          {t("chat.composer.hint")} {t("chat.composer.attachHint")}
        </p>
      </form>
      <AlertDialog open={!!confirming} onOpenChange={(o) => !o && !acting && setConfirming(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {confirming?.kind === "edit" ? t("chat.message.editTitle") : t("chat.message.deleteTitle")}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {confirming?.kind === "edit" ? t("chat.message.editBody") : t("chat.message.deleteBody")}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={acting}>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction
              variant={confirming?.kind === "delete" ? "destructive" : "default"}
              disabled={acting}
              onClick={() => {
                if (!confirming) return
                void (confirming.kind === "edit" ? editMessage(confirming.msg) : deleteMessage(confirming.msg))
              }}
            >
              {confirming?.kind === "edit" ? t("chat.message.edit") : t("chat.message.delete")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  )
}

// Starter prompts per built-in profile; user profiles and conversations
// without one get Explore's.
// i18n keys: chat.starters.explore chat.starters.accounts chat.starters.site-admin
// chat.starters.data-entry chat.starters.local-model (each p1 to p4)
const STARTER_SETS = new Set(["explore", "accounts", "site-admin", "data-entry", "local-model"])
const STARTER_KEYS = ["p1", "p2", "p3", "p4"] as const

/** An empty conversation: what it is for and four prompts that fill the composer (never send). */
function StarterPrompts({ profileID, disabled, onPick }: { profileID: string; disabled: boolean; onPick: (text: string) => void }) {
  const { t } = useTranslation()
  const set = STARTER_SETS.has(profileID) ? profileID : "explore"
  const headingID = React.useId()
  return (
    <div className="flex flex-col gap-3">
      <p className="text-muted-foreground text-sm">{t("chat.emptyConversation")}</p>
      <h2 id={headingID} className="text-sm font-medium">
        {t("chat.starters.title")}
      </h2>
      <ul className="grid gap-2 sm:grid-cols-2" aria-labelledby={headingID}>
        {STARTER_KEYS.map((k) => {
          const text = t(`chat.starters.${set}.${k}`)
          return (
            <li key={k}>
              <button
                type="button"
                disabled={disabled}
                onClick={() => onPick(text)}
                className="hover:bg-muted focus-visible:ring-ring/50 h-full w-full rounded-lg border px-3 py-2 text-start text-sm outline-none focus-visible:ring-3 disabled:opacity-50"
              >
                <span dir="auto">{text}</span>
              </button>
            </li>
          )
        })}
      </ul>
      <p className="text-muted-foreground text-xs">{t("chat.starters.hint")}</p>
    </div>
  )
}

/** File chips: staged ones with a remove button, sent ones without. */
function AttachmentChips({
  items,
  label,
  onRemove,
}: {
  items: StagedAttachment[]
  label: string
  onRemove?: (a: StagedAttachment) => void
}) {
  const { t } = useTranslation()
  return (
    <ul aria-label={label} className="flex flex-wrap justify-end gap-1.5">
      {items.map((a) => (
        <li key={a.id} className="bg-muted text-foreground flex max-w-64 items-center gap-1 rounded-md border px-2 py-0.5 text-xs">
          {a.kind === "image" ? (
            <IconPhoto className="size-3.5 shrink-0" aria-hidden />
          ) : a.kind === "document" ? (
            <IconFileTypePdf className="size-3.5 shrink-0" aria-hidden />
          ) : (
            <IconFileText className="size-3.5 shrink-0" aria-hidden />
          )}
          <span className="truncate" title={a.name}>
            {a.name}
          </span>
          {onRemove && (
            <button
              type="button"
              className="hover:text-destructive -me-1 shrink-0 rounded p-0.5"
              aria-label={t("chat.composer.remove", { name: a.name })}
              onClick={() => onRemove(a)}
            >
              <IconX className="size-3" aria-hidden />
            </button>
          )}
        </li>
      ))}
    </ul>
  )
}

function UserBubble({ text, attachments = [] }: { text: string; attachments?: StagedAttachment[] }) {
  const { t } = useTranslation()
  return (
    <div className="flex flex-col items-end gap-1">
      {attachments.length > 0 && <AttachmentChips items={attachments} label={t("chat.attachments.label")} />}
      {text && (
        <p dir="auto" className="bg-primary text-primary-foreground max-w-[85%] rounded-2xl px-3 py-2 text-sm break-words whitespace-pre-wrap">
          {text}
        </p>
      )}
    </div>
  )
}

function MessageView({ m }: { m: ChatMessage }) {
  if (m.role === "user") return <UserBubble text={m.text} attachments={m.attachments} />
  return (
    <div className={cn("flex flex-col gap-2")}>
      {m.tools.length > 0 && (
        <div className="flex flex-col gap-1.5">
          {m.tools.map((tc) => (
            <ToolRow
              key={tc.id}
              tool={tc.tool}
              site={tc.site}
              status={tc.status as ToolStatus}
              approval={tc.approval as ToolApproval}
              summary={tc.summary}
            />
          ))}
        </div>
      )}
      {m.text && <ChatMarkdown text={m.text} />}
    </div>
  )
}
