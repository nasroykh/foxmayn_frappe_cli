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
  KeyStatus,
  Model,
  PasswordRequest,
  Preview,
  ProviderInfo,
  RemoveResult,
  SignInProgress,
  Site,
  SiteList,
  UpdateInfo,
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
  KeyStatus,
  Model,
  PasswordRequest,
  Preview,
  ProviderInfo,
  RemoveResult,
  SignInProgress,
  Site,
  SiteList,
  UpdateInfo,
  Validation,
  WSLInfo,
}

// ---- The assistant ----
// The generated types say `string` for the closed sets and `T[] | null` for
// lists; these narrow them. backend.ts fills in the empty lists, so the UI
// never meets null.

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

export interface ConversationDetail extends Omit<GeneratedConversationDetail, "messages"> {
  messages: ChatMessage[]
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

  listProviders(): Promise<ProviderInfo[]>
  saveProvider(p: ProviderInfo): Promise<ProviderInfo>
  deleteProvider(id: string): Promise<void>
  /** Saves the key in the OS keychain and checks it; a rejected key is not kept (code "auth"). */
  setKey(providerID: string, key: string): Promise<void>
  keyStatus(providerID: string): Promise<KeyStatus>
  /** Looks for Ollama and LM Studio on this computer; saves nothing. */
  detectLocal(): Promise<ProviderInfo[]>
  listModels(providerID: string): Promise<Model[]>

  onChatDelta(cb: (ev: ChatDelta) => void): () => void
  onChatTool(cb: (ev: ChatTool) => void): () => void
  onChatApproval(cb: (ev: ChatApproval) => void): () => void
  onChatApprovalClosed(cb: (ev: ChatApprovalClosed) => void): () => void
  onChatUsage(cb: (ev: ChatUsage) => void): () => void
  onChatDone(cb: (ev: ChatDone) => void): () => void
  onChatError(cb: (ev: ChatError) => void): () => void

  onConfigChanged(cb: (ev: ConfigChanged) => void): () => void
  onSignInProgress(cb: (ev: SignInProgress) => void): () => void
  onInstallerLog(cb: (line: string) => void): () => void

  copyText(text: string): Promise<void>
}
