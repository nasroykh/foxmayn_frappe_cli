import { IconMessageChatbot, IconPlus } from "@tabler/icons-react"
import * as React from "react"
import { useTranslation } from "react-i18next"

import { useApp } from "@/app/app-context"
import { LoadError } from "@/components/page"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Skeleton } from "@/components/ui/skeleton"
import { toast } from "@/components/ui/toast"
import { backend } from "@/lib/backend"
import type { Conversation, ProviderInfo } from "@/lib/backend-types"
import { appError, errorTitle, type AppError } from "@/lib/errors"
import { AssistantOnboarding } from "@/screens/assistant/assistant-onboarding"
import { ChatPane } from "@/screens/assistant/chat-pane"
import { ConversationList } from "@/screens/assistant/conversation-list"
import { NewConversationForm } from "@/screens/assistant/new-conversation-form"
import { usable } from "@/screens/assistant/provider-parts"

// The conversation open when the user last left the screen.
let lastSelected = ""

/** The Assistant: chat with a model that works on a site, or onboarding when no provider is usable. */
export function AssistantScreen() {
  const { t } = useTranslation()
  const { sites, addSite } = useApp()
  const [providers, setProviders] = React.useState<ProviderInfo[] | null>(null)
  const [conversations, setConversations] = React.useState<Conversation[] | null>(null)
  const [error, setError] = React.useState<AppError | null>(null)
  const [selected, setSelectedState] = React.useState(lastSelected)
  const [newOpen, setNewOpen] = React.useState(false)
  // Decided once, from the first answer: a key saved mid-way must not end the onboarding before the chat starts.
  const [onboarding, setOnboarding] = React.useState<boolean | null>(null)

  const select = React.useCallback((id: string) => {
    lastSelected = id
    setSelectedState(id)
  }, [])

  const reloadProviders = React.useCallback(async () => {
    try {
      setProviders(await backend.listProviders())
    } catch (err) {
      setError(appError(err))
    }
  }, [])

  const reloadConversations = React.useCallback(async () => {
    try {
      setConversations(await backend.listConversations())
    } catch (err) {
      setError(appError(err))
    }
  }, [])

  const reload = React.useCallback(() => {
    setError(null)
    void reloadProviders()
    void reloadConversations()
  }, [reloadProviders, reloadConversations])

  React.useEffect(reload, [reload])

  // Select the newest conversation when none (or a deleted one) is selected.
  React.useEffect(() => {
    if (!conversations) return
    if (!conversations.some((c) => c.id === selected)) select(conversations[0]?.id ?? "")
  }, [conversations, selected, select])

  const siteList = sites.data?.sites ?? []
  const good = (providers ?? []).filter(usable)
  React.useEffect(() => {
    if (providers && onboarding === null) setOnboarding(good.length === 0)
  }, [providers, onboarding, good.length])

  async function remove(c: Conversation) {
    try {
      await backend.deleteConversation(c.id)
      await reloadConversations()
    } catch (err) {
      const e = appError(err)
      toast.add({ title: errorTitle(e), description: e.message, type: "error" })
    }
  }

  if (error && (!providers || !conversations)) {
    return (
      <div className="p-6">
        <LoadError title={t("chat.loadFailed")} error={error} onRetry={reload} />
      </div>
    )
  }
  if (!providers || !conversations || !sites.data || onboarding === null) {
    return (
      <div className="flex flex-col gap-3 p-6">
        <Skeleton className="h-8 w-48" />
        <Skeleton className="h-24 w-full" />
      </div>
    )
  }

  if (onboarding) {
    return (
      <div className="h-full overflow-y-auto">
        <AssistantOnboarding
          sites={siteList}
          defaultSite={sites.data?.defaultSite}
          providers={providers}
          onProvidersChanged={() => void reloadProviders()}
          onDone={(c) => {
            select(c.id)
            setOnboarding(false)
            void reloadConversations()
            void reloadProviders()
          }}
        />
      </div>
    )
  }

  if (siteList.length === 0) {
    return (
      <Empty className="h-full">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <IconMessageChatbot />
          </EmptyMedia>
          <EmptyTitle>{t("chat.noSitesTitle")}</EmptyTitle>
          <EmptyDescription>{t("chat.noSites")}</EmptyDescription>
        </EmptyHeader>
        <EmptyContent>
          <Button onClick={addSite}>
            <IconPlus data-icon="inline-start" />
            {t("chat.addSite")}
          </Button>
        </EmptyContent>
      </Empty>
    )
  }

  const current = conversations.find((c) => c.id === selected)

  return (
    <div className="flex h-full min-h-0">
      <ConversationList
        conversations={conversations}
        selected={selected}
        onSelect={select}
        onNew={() => setNewOpen(true)}
        onDelete={remove}
      />
      {current ? (
        <ChatPane key={current.id} conv={current} providers={providers} onChanged={() => void reloadConversations()} />
      ) : (
        <Empty className="flex-1">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <IconMessageChatbot />
            </EmptyMedia>
            <EmptyTitle>{t("chat.noConversation")}</EmptyTitle>
            <EmptyDescription>{t("chat.noConversationHint")}</EmptyDescription>
          </EmptyHeader>
          <EmptyContent>
            <Button onClick={() => setNewOpen(true)}>
              <IconPlus data-icon="inline-start" />
              {t("chat.list.new")}
            </Button>
          </EmptyContent>
        </Empty>
      )}

      <Dialog open={newOpen} onOpenChange={setNewOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t("chat.new.title")}</DialogTitle>
            <DialogDescription>{t("chat.new.body")}</DialogDescription>
          </DialogHeader>
          <NewConversationForm
            sites={siteList}
            defaultSite={sites.data?.defaultSite}
            providers={good}
            onCreated={(c) => {
              setNewOpen(false)
              select(c.id)
              void reloadConversations()
            }}
          />
        </DialogContent>
      </Dialog>
    </div>
  )
}
