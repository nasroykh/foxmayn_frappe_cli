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
  CheckResult,
  ConfigChanged,
  ConnectRequest,
  Environment,
  FFCInfo,
  PasswordRequest,
  Preview,
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
  CheckResult,
  ConfigChanged,
  ConnectRequest,
  Environment,
  FFCInfo,
  PasswordRequest,
  Preview,
  RemoveResult,
  SignInProgress,
  Site,
  SiteList,
  UpdateInfo,
  Validation,
  WSLInfo,
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

  onConfigChanged(cb: (ev: ConfigChanged) => void): () => void
  onSignInProgress(cb: (ev: SignInProgress) => void): () => void
  onInstallerLog(cb: (line: string) => void): () => void

  copyText(text: string): Promise<void>
}
