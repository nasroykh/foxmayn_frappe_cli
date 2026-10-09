// The real backend: the Wails bindings of the Go services.
import { Clipboard, Events } from "@wailsio/runtime"

import {
  AppService,
  AssistantService,
  AssistantsService,
  SitesService,
} from "../../bindings/github.com/nasroykh/foxmayn_frappe_cli/desktop/services"
import type * as wire from "../../bindings/github.com/nasroykh/foxmayn_frappe_cli/desktop/services/models"
import type {
  AttachmentKind,
  Backend,
  Cancellable,
  ChatApproval,
  ChatMessage,
  ConversationDetail,
  OpenRouterAuth,
  Profile,
  PromptPreview,
  StagedAttachment,
  Toolset,
} from "@/lib/backend-types"

// A new promise (never p itself), so setting cancel on it leaves p untouched.
function cancellable<T>(p: Promise<T> & { cancel(): unknown }): Cancellable<T> {
  const out = p.then((x) => x) as Cancellable<T>
  out.cancel = () => {
    void p.cancel()
  }
  return out
}

// The generated types allow null lists (Go's nil slices) and a raw args value;
// the UI gets plain arrays and an object.
function approval(a: wire.ChatApproval): ChatApproval {
  const raw = a.args as unknown
  let args: Record<string, unknown> = {}
  if (raw && typeof raw === "object") args = raw as Record<string, unknown>
  else if (typeof raw === "string") {
    try {
      args = JSON.parse(raw) as Record<string, unknown>
    } catch {
      args = {}
    }
  }
  return {
    ...a,
    kind: a.kind === "ffc" ? "ffc" : "app",
    args,
    doctypes: a.doctypes ?? [],
    names: a.names ?? [],
    diff: a.diff ?? [],
    noChanges: a.noChanges ?? false,
    message: a.message ?? "",
  }
}

function staged(a: wire.StagedAttachment): StagedAttachment {
  return { ...a, kind: (a.kind === "image" ? "image" : "text") as AttachmentKind }
}

function detail(d: wire.ConversationDetail): ConversationDetail {
  const messages: ChatMessage[] = (d.messages ?? []).map((m) => ({
    ...m,
    role: m.role === "assistant" ? "assistant" : "user",
    tools: m.tools ?? [],
    attachments: (m.attachments ?? []).map(staged),
  }))
  return { ...d, messages, runUsage: d.runUsage ?? [] }
}

function profile(p: wire.Profile): Profile {
  return {
    ...p,
    mode: p.mode === "ask" ? "ask" : "read",
    toolsets: (p.toolsets ?? []) as Toolset[],
    allowTools: p.allowTools ?? [],
    allowDoctypes: p.allowDoctypes ?? [],
    denyDoctypes: p.denyDoctypes ?? [],
    allowMethods: p.allowMethods ?? [],
    denyMethods: p.denyMethods ?? [],
    denyTools: p.denyTools ?? [],
  }
}

function preview(p: wire.PromptPreview): PromptPreview {
  return { ...p, mode: p.mode === "ask" ? "ask" : "read", tools: p.tools ?? [] }
}

export const backend: Backend = {
  environment: () => AppService.Environment(),
  refreshFFC: () => AppService.RefreshFFC(),
  installFFC: () => cancellable(AppService.InstallFFC()),
  openWebsite: (url) => AppService.OpenWebsite(url),
  checkForUpdate: () => AppService.CheckForUpdate(),
  openConfigFolder: () => AppService.OpenConfigFolder(),
  openFFCFolder: () => AppService.OpenFFCFolder(),
  setWindowTheme: (dark) => AppService.SetWindowTheme(dark),

  listSites: () => SitesService.List(),
  validate: (name, url) => SitesService.Validate(name, url),
  addWithAPIKey: (req) => SitesService.AddWithAPIKey(req),
  addWithPassword: (req) => SitesService.AddWithPassword(req),
  signInWithBrowser: (req) => SitesService.SignInWithBrowser(req),
  cancelSignIn: () => SitesService.CancelSignIn(),
  reopenSignInPage: () => SitesService.ReopenSignInPage(),
  check: (name) => SitesService.Check(name),
  setDefault: (name) => SitesService.SetDefault(name),
  rename: (oldName, newName) => SitesService.Rename(oldName, newName),
  changeURL: (name, url) => SitesService.ChangeURL(name, url),
  remove: (name) => SitesService.Remove(name),

  listAssistants: () => AssistantsService.List(),
  preview: (req) => AssistantsService.Preview(req),
  connect: (req) => AssistantsService.Connect(req),
  previewDisconnect: (client) => AssistantsService.PreviewDisconnect(client),
  disconnect: (client) => AssistantsService.Disconnect(client),

  sendMessage: (convID, text, attachmentIDs) => AssistantService.Send(convID, text, attachmentIDs ?? []),
  addAttachment: async (convID) => {
    const r = await AssistantService.AddAttachment(convID)
    return { attachments: (r.attachments ?? []).map(staged), errors: r.errors ?? [], cancelled: r.cancelled ?? false }
  },
  addPastedImage: async (convID, base64) => staged(await AssistantService.AddPastedImage(convID, base64)),
  removeAttachment: (convID, id) => AssistantService.RemoveAttachment(convID, id),
  listAttachments: async (convID) => ((await AssistantService.ListAttachments(convID)) ?? []).map(staged),
  cancelRun: (runID) => AssistantService.Cancel(runID),
  answerApproval: (convID, approvalID, approve) => AssistantService.Answer(convID, approvalID, approve),
  continueRun: (runID) => AssistantService.Continue(runID),
  pendingApprovals: async (convID) => ((await AssistantService.PendingApprovals(convID)) ?? []).map(approval),
  newConversation: (site, mode, providerID, model) => AssistantService.NewConversation(site, mode, providerID, model),
  listConversations: async () => (await AssistantService.ListConversations()) ?? [],
  getConversation: async (id) => detail(await AssistantService.GetConversation(id)),
  deleteConversation: (id) => AssistantService.DeleteConversation(id),
  setConversationMode: (id, mode) => AssistantService.SetConversationMode(id, mode),
  renameConversation: (id, title) => AssistantService.Rename(id, title),

  listProviders: async () => (await AssistantService.ListProviders()) ?? [],
  saveProvider: (p) => AssistantService.SaveProvider(p),
  deleteProvider: (id) => AssistantService.DeleteProvider(id),
  setKey: (providerID, key) => AssistantService.SetKey(providerID, key),
  keyStatus: (providerID) => AssistantService.KeyStatus(providerID),
  signInOpenRouter: (providerID, attempt) => AssistantService.SignInOpenRouter(providerID, attempt),
  cancelOpenRouterSignIn: () => AssistantService.CancelSignIn(),
  detectLocal: async () => (await AssistantService.DetectLocal()) ?? [],
  listModels: async (providerID) => (await AssistantService.ListModels(providerID)) ?? [],

  listPresets: async () => ((await AssistantService.ListPresets()) ?? []).map(profile),
  listProfiles: async () => ((await AssistantService.ListProfiles()) ?? []).map(profile),
  saveProfile: async (p) => profile(await AssistantService.SaveProfile(p)),
  deleteProfile: (id) => AssistantService.DeleteProfile(id),
  setConversationProfile: (convID, profileID) => AssistantService.SetConversationProfile(convID, profileID),
  getSiteSettings: (site) => AssistantService.GetSiteSettings(site),
  saveSiteSettings: (s) => AssistantService.SaveSiteSettings(s),
  promptPreview: async (convID) => preview(await AssistantService.PromptPreview(convID)),

  search: async (query, filter, limit) => (await AssistantService.Search(query, filter, limit)) ?? [],
  pinConversation: (convID, pinned) => AssistantService.Pin(convID, pinned),
  archiveConversation: (convID, archived) => AssistantService.Archive(convID, archived),
  getRetention: async () => {
    const days = await AssistantService.GetRetention()
    return days === 30 || days === 90 ? days : 0
  },
  setRetention: (days) => AssistantService.SetRetention(days),
  exportConversation: (convID, format) => AssistantService.ExportConversation(convID, format),
  importConversation: () => AssistantService.ImportConversation(),

  onChatDelta: (cb) => Events.On("chat:delta", (ev) => cb(ev.data)),
  onChatTool: (cb) => Events.On("chat:tool", (ev) => cb(ev.data)),
  onChatApproval: (cb) => Events.On("chat:approval", (ev) => cb(approval(ev.data))),
  onChatApprovalClosed: (cb) => Events.On("chat:approval-closed", (ev) => cb(ev.data)),
  onChatUsage: (cb) => Events.On("chat:usage", (ev) => cb(ev.data)),
  onChatTitle: (cb) => Events.On("chat:title", (ev) => cb(ev.data)),
  onChatDone: (cb) => Events.On("chat:done", (ev) => cb(ev.data)),
  onChatError: (cb) => Events.On("chat:error", (ev) => cb(ev.data)),
  onChatAttachments: (cb) =>
    Events.On("chat:attachments", (ev) =>
      cb({ ...ev.data, attachments: (ev.data.attachments ?? []).map(staged), errors: ev.data.errors ?? [] }),
    ),
  onOpenRouterAuth: (cb) => Events.On("auth:openrouter", (ev) => cb(ev.data as OpenRouterAuth)),

  onConfigChanged: (cb) => Events.On("config:changed", (ev) => cb(ev.data)),
  onSignInProgress: (cb) => Events.On("signin:progress", (ev) => cb(ev.data)),
  onInstallerLog: (cb) => Events.On("installer:log", (ev) => cb(ev.data.line)),

  copyText: (text) => Clipboard.SetText(text),
}
