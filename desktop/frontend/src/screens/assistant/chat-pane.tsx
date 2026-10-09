import { IconAlertTriangle, IconCheck, IconPencil, IconPlayerPlay, IconPlayerStop, IconSend, IconX } from "@tabler/icons-react"
import * as React from "react"
import { useTranslation } from "react-i18next"
import { cn } from "cn"

import { ApprovalCard } from "@/components/chat/approval-card"
import { ChatMarkdown } from "@/components/chat/markdown"
import { ToolRow } from "@/components/chat/tool-row"
import { UsageLine, useCostText } from "@/components/chat/usage-line"
import { Alert, AlertAction, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Textarea } from "@/components/ui/textarea"
import { toast } from "@/components/ui/toast"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { backend } from "@/lib/backend"
import type {
  ChatMessage,
  Conversation,
  ConversationMode,
  ProviderInfo,
  RunUsage,
  ToolApproval,
  ToolStatus,
  UsageTotals,
} from "@/lib/backend-types"
import { hasUsage, noUsage } from "@/lib/cost"
import { appError, errorTitle, type AppError } from "@/lib/errors"
import { chatReducer, initialState } from "@/screens/assistant/chat-reducer"
import { ProfilePicker } from "@/screens/assistant/profile-picker"

function notify(err: unknown) {
  const e = appError(err)
  toast.add({ title: errorTitle(e), description: e.message, type: "error" })
}

const OVERLAYS = '[role="dialog"],[role="alertdialog"],[role="menu"],[role="listbox"]'

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
  const [runUsage, setRunUsage] = React.useState<RunUsage[]>([])
  const [total, setTotal] = React.useState<UsageTotals>(noUsage)
  const [renaming, setRenaming] = React.useState<string | null>(null)
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
    setMode(conv.mode === "ask" ? "ask" : "read")
    void load()
    // conv.mode only seeds the first paint; load() sets the real one.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [convID, load])

  // The chat events. Cleaned up on unmount; the reducer drops other runs' events.
  React.useEffect(() => {
    const offs = [
      backend.onChatDelta((ev) => dispatch({ type: "delta", convID: ev.convID, runID: ev.runID, text: ev.text })),
      backend.onChatTool((ev) => dispatch({ type: "tool", ev })),
      backend.onChatUsage((ev) => dispatch({ type: "usage", ev })),
      backend.onChatTitle((ev) => {
        if (ev.convID !== convRef.current) return
        onChangedRef.current()
        // The title call is part of the total; a reload during a run would reset the live view.
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

  async function send() {
    const text = draft.trim()
    if (!text || active) return
    setDraft("")
    reloadAfterStart.current = false
    dispatch({ type: "sending", text })
    try {
      const runID = await backend.sendMessage(convID, text)
      dispatch({ type: "started", runID })
      // The run ended before its id came back: the store has the rest.
      if (reloadAfterStart.current) {
        reloadAfterStart.current = false
        void load()
      }
    } catch (err) {
      const e = appError(err)
      setDraft(text)
      dispatch({ type: "sendFailed", error: { code: e.code, message: e.message, detail: e.detail } })
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
    if (!name || name === conv.title) {
      setRenaming(null)
      return
    }
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
            {hasUsage(total) && (
              <span title={t("chat.header.totalHelp")} data-testid="usage-total">
                {" · "}
                {t("chat.header.total", {
                  usage: [
                    t("chat.usage.tokens", { input: total.input.toLocaleString(), output: total.output.toLocaleString() }),
                    costText(total),
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
            <p className="text-muted-foreground text-sm">{t("chat.emptyConversation")}</p>
          )}
          {messages?.map((m) => (
            <React.Fragment key={m.id}>
              <MessageView m={m} />
              {usageOf.has(m.id) && <UsageLine usage={usageOf.get(m.id)!} className="-mt-2" />}
            </React.Fragment>
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
                <p>{state.error.message}</p>
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
        className="border-t p-3"
        onSubmit={(e) => {
          e.preventDefault()
          void send()
        }}
      >
        <div className="mx-auto flex max-w-3xl items-end gap-2">
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
            <Button type="submit" disabled={!draft.trim()} aria-label={t("chat.composer.sendLabel")}>
              <IconSend data-icon="inline-start" />
              {t("chat.composer.send")}
            </Button>
          )}
        </div>
        <p className="text-muted-foreground mx-auto mt-1 max-w-3xl text-xs">{t("chat.composer.hint")}</p>
      </form>
    </section>
  )
}

function UserBubble({ text }: { text: string }) {
  return (
    <div className="flex justify-end">
      <p className="bg-primary text-primary-foreground max-w-[85%] rounded-2xl px-3 py-2 text-sm break-words whitespace-pre-wrap">
        {text}
      </p>
    </div>
  )
}

function MessageView({ m }: { m: ChatMessage }) {
  if (m.role === "user") return <UserBubble text={m.text} />
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
