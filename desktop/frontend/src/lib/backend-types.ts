// The app's view of the Go services. The real implementation (backend.ts)
// calls the Wails bindings; in `vite --mode mock` the alias in vite.config.ts
// swaps it for src/mock/backend.ts, which never reaches a production build.
import type {
  AddedSite,
  APIKeyRequest,
  ApplyResult,
  Assistant,
  AssistantList,
  BrowserSignInRequest,
  ChatApproval as GeneratedChatApproval,
  ChatApprovalClosed,
  ChatDelta,
  ChatDone,
  ChatError,
  ChatMessage as GeneratedChatMessage,
  ChatTitle,
  ChatTool,
  ChatToolCall,
  ChatUsage,
  CheckResult,
  ConfigChanged,
  ConnectRequest,
  Conversation,
  ConversationDetail as GeneratedConversationDetail,
  DiffField,
  Environment,
  FFCInfo,
  FFCUpdate,
  ImportResult,
  KeyStatus,
  Model,
  OpenRouterAuth as GeneratedOpenRouterAuth,
  PasswordRequest,
  Preview,
  Profile as GeneratedProfile,
  PromptPreview as GeneratedPromptPreview,
  ProviderInfo,
  RemoveResult,
  RunUsage,
  SearchFilter,
  SearchHit,
  SignInProgress,
  Site,
  SiteList,
  SiteSettings,
  UpdateInfo,
  UsageTotals,
  Validation,
  WSLInfo,
} from "../../bindings/github.com/nasroykh/foxmayn_frappe_cli/desktop/services/models"

export type {
  AddedSite,
  APIKeyRequest,
  ApplyResult,
  Assistant,
  AssistantList,
  BrowserSignInRequest,
  ChatApprovalClosed,
  ChatDelta,
  ChatDone,
  ChatError,
  ChatTitle,
  ChatTool,
  ChatToolCall,
  ChatUsage,
  CheckResult,
  ConfigChanged,
  ConnectRequest,
  Conversation,
  DiffField,
  Environment,
  FFCInfo,
  FFCUpdate,
  ImportResult,
  KeyStatus,
  Model,
  PasswordRequest,
  Preview,
  ProviderInfo,
  RemoveResult,
  RunUsage,
  SearchFilter,
  SearchHit,
  SignInProgress,
  Site,
  SiteList,
  SiteSettings,
  UpdateInfo,
  UsageTotals,
  Validation,
  WSLInfo,
}

// ---- The assistant ----
// The generated types say `string` for the closed sets and `T[] | null` for
// lists; these narrow them. backend.ts fills in the empty lists, so the UI
// never meets null.

/** How long a conversation is kept after its last message; 0 is forever. */
export type RetentionDays = 0 | 30 | 90
/** The formats ExportConversation writes. */
export type ExportFormat = "json" | "md"
/** What a conversation may do to its site. */
export type ConversationMode = "read" | "ask"
/** The kinds of provider SaveProvider accepts. */
export type ProviderKind = "anthropic" | "openrouter" | "ollama" | "lmstudio" | "custom" | "openai" | "gemini"
/** A tool call's state: chat:tool status and ChatToolCall.status. */
export type ToolStatus = "running" | "ok" | "error" | "stopped"
/** How a run ended (chat:done status). "paused" waits for continueRun. */
export type RunStatus = "done" | "paused" | "cancelled" | "error"
/** "app" is the app's own card, "ffc" is ffc's own confirmation question. */
export type ApprovalKind = "app" | "ffc"
/** chat:approval-closed outcome. */
export type ApprovalOutcome = "approved" | "declined" | "cancelled"
/** ChatToolCall.approval: "" when the call needed none. */
export type ToolApproval = "" | "approved" | "declined" | "cancelled" | "ffc-approved" | "ffc-declined"

/** An approval card. `args` is the exact JSON the call will run with. */
export interface ChatApproval
  extends Omit<GeneratedChatApproval, "kind" | "args" | "doctypes" | "names" | "diff" | "noChanges" | "message"> {
  kind: ApprovalKind
  args: Record<string, unknown>
  doctypes: string[]
  names: string[]
  /** The changed fields of an update_doc; empty with noChanges. */
  diff: DiffField[]
  /** update_doc whose data already matches the document. */
  noChanges: boolean
  /** ffc's question, for kind "ffc". */
  message: string
}

/** A visible message. Tool results and thinking are never sent. */
export interface ChatMessage extends Omit<GeneratedChatMessage, "role" | "tools"> {
  role: "user" | "assistant"
  tools: ChatToolCall[]
}

export interface ConversationDetail extends Omit<GeneratedConversationDetail, "messages" | "runUsage"> {
  messages: ChatMessage[]
  /** Tokens and cost of each run, shown under the run's last message (msgID). */
  runUsage: RunUsage[]
}

/** ffc's tool sets a profile can serve; none chosen means core and lifecycle. */
export type Toolset = "core" | "lifecycle" | "collab" | "admin" | "files" | "erp"

type ProfileLists = "toolsets" | "allowTools" | "allowDoctypes" | "denyDoctypes" | "allowMethods" | "denyMethods" | "denyTools"

/**
 * A profile: a built-in preset (preset true, never changed; duplicate it to
 * edit) or the user's own. It only narrows the site's policy. Empty lists
 * mean no limit of the profile's own.
 */
export interface Profile extends Omit<GeneratedProfile, "mode" | ProfileLists> {
  mode: ConversationMode
  toolsets: Toolset[]
  allowTools: string[]
  allowDoctypes: string[]
  denyDoctypes: string[]
  allowMethods: string[]
  denyMethods: string[]
  denyTools: string[]
}

/** What the model gets at a conversation's next run. */
export interface PromptPreview extends Omit<GeneratedPromptPreview, "mode" | "tools"> {
  /** The stricter of the profile's mode and the conversation's switch. */
  mode: ConversationMode
  tools: string[]
}

/** An OpenRouter browser sign-in step; the last one is done, cancelled or failed. */
export type OpenRouterAuthStatus = "browser" | "exchanging" | "verifying" | "done" | "cancelled" | "failed"

/** auth:openrouter. It never carries the key. */
export interface OpenRouterAuth extends Omit<GeneratedOpenRouterAuth, "status"> {
  status: OpenRouterAuthStatus
}

/** A promise the caller can cancel (the Go side sees its context end). */
export type Cancellable<T> = Promise<T> & { cancel(): void }

export interface Backend {
  environment(): Promise<Environment>
  refreshFFC(): Promise<FFCInfo>
  installFFC(): Cancellable<FFCInfo>
  openWebsite(url: string): Promise<void>
  checkForUpdate(): Promise<UpdateInfo>
  openConfigFolder(): Promise<void>
  openFFCFolder(): Promise<void>
  setWindowTheme(dark: boolean): Promise<void>

  listSites(): Promise<SiteList>
  validate(name: string, url: string): Promise<Validation>
  addWithAPIKey(req: APIKeyRequest): Promise<AddedSite>
  addWithPassword(req: PasswordRequest): Promise<AddedSite>
  signInWithBrowser(req: BrowserSignInRequest): Promise<AddedSite>
  cancelSignIn(): Promise<void>
  reopenSignInPage(): Promise<void>
  check(name: string): Promise<CheckResult>
  setDefault(name: string): Promise<void>
  rename(oldName: string, newName: string): Promise<void>
  changeURL(name: string, url: string): Promise<string>
  remove(name: string): Promise<RemoveResult>

  listAssistants(): Promise<AssistantList>
  preview(req: ConnectRequest): Promise<Preview>
  connect(req: ConnectRequest): Promise<ApplyResult>
  previewDisconnect(client: string): Promise<Preview>
  disconnect(client: string): Promise<ApplyResult>

  // Assistant. Failures are *services.Error (code, message, ...); read them
  // with appError. No call ever returns an API key.
  /** Adds the message and starts a run; answers with the run id at once. */
  sendMessage(convID: string, text: string): Promise<string>
  /** Stops a run (an open card counts as declined). */
  cancelRun(runID: string): Promise<void>
  /** Settles a card; refused (not_found) for a card of another conversation. */
  answerApproval(convID: string, approvalID: string, approve: boolean): Promise<void>
  /** Resumes a run that ended "paused". */
  continueRun(runID: string): Promise<void>
  /** Open cards, to show again after a reload. */
  pendingApprovals(convID: string): Promise<ChatApproval[]>
  newConversation(site: string, mode: ConversationMode, providerID: string, model: string): Promise<Conversation>
  listConversations(): Promise<Conversation[]>
  getConversation(id: string): Promise<ConversationDetail>
  deleteConversation(id: string): Promise<void>
  /** Refused (invalid) while a run is active in the conversation. */
  setConversationMode(id: string, mode: ConversationMode): Promise<void>
  /** A title the user chose; no automatic title replaces it. Empty is refused (invalid). */
  renameConversation(id: string, title: string): Promise<void>

  listProviders(): Promise<ProviderInfo[]>
  saveProvider(p: ProviderInfo): Promise<ProviderInfo>
  deleteProvider(id: string): Promise<void>
  /** Saves the key in the OS keychain and checks it; a rejected key is not kept (code "auth"). */
  setKey(providerID: string, key: string): Promise<void>
  keyStatus(providerID: string): Promise<KeyStatus>
  /**
   * Gets an OpenRouter key through the browser (PKCE), checks it like setKey
   * and saves it in the keychain; progress comes as onOpenRouterAuth, tagged
   * with attempt. A cancel after the key exchange no longer stops the save.
   * Cancelled (code "cancelled") by cancelOpenRouterSignIn.
   */
  signInOpenRouter(providerID: string, attempt: string): Promise<ProviderInfo>
  cancelOpenRouterSignIn(): Promise<void>
  /** Looks for Ollama and LM Studio on this computer; saves nothing. */
  detectLocal(): Promise<ProviderInfo[]>
  listModels(providerID: string): Promise<Model[]>

  /** The built-in profiles. */
  listPresets(): Promise<Profile[]>
  /** The user's own profiles. */
  listProfiles(): Promise<Profile[]>
  /** Adds (empty id) or changes a profile; a preset is refused (invalid). */
  saveProfile(p: Profile): Promise<Profile>
  /** Its conversations move to the Explore preset (read only). */
  deleteProfile(id: string): Promise<void>
  /** "" for none. Refused while a run is active, and for a cloud provider on a local-only site. */
  setConversationProfile(convID: string, profileID: string): Promise<Conversation>
  getSiteSettings(site: string): Promise<SiteSettings>
  saveSiteSettings(s: SiteSettings): Promise<SiteSettings>
  /** Opens an engine session (may sign in) but calls no tool. */
  promptPreview(convID: string): Promise<PromptPreview>

  /** Finds messages by their words; a date filter is "YYYY-MM-DD". Archived conversations only with filter.archived. */
  search(query: string, filter: SearchFilter, limit: number): Promise<SearchHit[]>
  /** Does not change the conversation's updated time. */
  pinConversation(convID: string, pinned: boolean): Promise<void>
  archiveConversation(convID: string, archived: boolean): Promise<void>
  getRetention(): Promise<RetentionDays>
  /** Older conversations go at the next start and then daily, not at once. */
  setRetention(days: RetentionDays): Promise<void>
  /** Asks where to save; answers the path, or "" when the person cancelled. */
  exportConversation(convID: string, format: ExportFormat): Promise<string>
  /** Asks for a file; adds a new conversation (read only). */
  importConversation(): Promise<ImportResult>

  onChatDelta(cb: (ev: ChatDelta) => void): () => void
  onChatTool(cb: (ev: ChatTool) => void): () => void
  onChatApproval(cb: (ev: ChatApproval) => void): () => void
  onChatApprovalClosed(cb: (ev: ChatApprovalClosed) => void): () => void
  onChatUsage(cb: (ev: ChatUsage) => void): () => void
  /** The model named the conversation (after its first answer). */
  onChatTitle(cb: (ev: ChatTitle) => void): () => void
  onChatDone(cb: (ev: ChatDone) => void): () => void
  onChatError(cb: (ev: ChatError) => void): () => void
  onOpenRouterAuth(cb: (ev: OpenRouterAuth) => void): () => void

  onConfigChanged(cb: (ev: ConfigChanged) => void): () => void
  onSignInProgress(cb: (ev: SignInProgress) => void): () => void
  onInstallerLog(cb: (line: string) => void): () => void

  copyText(text: string): Promise<void>
}
