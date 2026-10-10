import {
  IconAlertTriangle,
  IconArrowLeft,
  IconArrowRight,
  IconBrowser,
  IconChevronDown,
  IconCircleCheck,
  IconExternalLink,
  IconKey,
  IconLockOpen,
  IconPlugConnected,
  IconUser,
} from "@tabler/icons-react"
import * as React from "react"
import { Trans, useTranslation } from "react-i18next"
import type { TFunction } from "i18next"

import { useApp } from "@/app/app-context"
import { copy, CopyField } from "@/components/copy-field"
import { Details } from "@/components/page"
import { SecretInput } from "@/components/secret-input"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
  FieldSet,
  FieldLegend,
  FieldTitle,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Progress, ProgressLabel, ProgressValue } from "@/components/ui/progress"
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group"
import { Sheet, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import { toast } from "@/components/ui/toast"
import { backend } from "@/lib/backend"
import type { AddedSite, SignInProgress, Validation } from "@/lib/backend-types"
import { appError, type AppError } from "@/lib/errors"

type Method = "oauth" | "apikey" | "password"
type Step = 1 | 2 | 3 | 4

function stepTitle(t: TFunction, step: Step) {
  switch (step) {
    case 1:
      return t("addSite.stepTitle.site")
    case 2:
      return t("addSite.stepTitle.method")
    case 3:
      return t("addSite.stepTitle.signIn")
    default:
      return t("addSite.stepTitle.done")
  }
}

/** "https://erp.acme.example" -> "erp", a starting point for the name. */
function suggestName(url: string) {
  const host = url
    .trim()
    .replace(/^https?:\/\//i, "")
    .split(/[/:?#]/)[0]
  if (!host) return ""
  const first = host.split(".")[0]
  return first.replace(/[^A-Za-z0-9._-]/g, "-").replace(/^[^A-Za-z0-9]+/, "")
}

export function AddSiteSheet({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const { t } = useTranslation()
  const { reloadSites, connectAssistant, sites } = useApp()
  const [step, setStep] = React.useState<Step>(1)
  const [url, setURL] = React.useState("")
  const [name, setName] = React.useState("")
  const [nameTouched, setNameTouched] = React.useState(false)
  const [replace, setReplace] = React.useState(false)
  const [validation, setValidation] = React.useState<Validation | null>(null)
  const [showErrors, setShowErrors] = React.useState(false)

  const [method, setMethod] = React.useState<Method>("oauth")
  const [advanced, setAdvanced] = React.useState(false)
  const [clientID, setClientID] = React.useState("")
  const [clientSecret, setClientSecret] = React.useState("")

  const [apiKey, setAPIKey] = React.useState("")
  const [apiSecret, setAPISecret] = React.useState("")
  const [username, setUsername] = React.useState("")
  const [password, setPassword] = React.useState("")

  const [busy, setBusy] = React.useState(false)
  const [progress, setProgress] = React.useState<SignInProgress | null>(null)
  const [error, setError] = React.useState<AppError | null>(null)
  const [added, setAdded] = React.useState<AddedSite | null>(null)
  // session changes on every open and close, so a call that was started
  // before the sheet closed cannot write into a reopened form. attempt names
  // the browser sign-in whose progress events are shown.
  const session = React.useRef(0)
  const attempt = React.useRef("")
  const [isDefault, setIsDefault] = React.useState(false)

  // Start fresh each time the sheet opens.
  React.useEffect(() => {
    session.current++
    attempt.current = ""
    if (!open) return
    setStep(1)
    setURL("")
    setName("")
    setNameTouched(false)
    setReplace(false)
    setValidation(null)
    setShowErrors(false)
    setMethod("oauth")
    setAdvanced(false)
    setClientID("")
    setClientSecret("")
    setAPIKey("")
    setAPISecret("")
    setUsername("")
    setPassword("")
    setBusy(false)
    setProgress(null)
    setError(null)
    setAdded(null)
  }, [open])

  // Validate name and address as the user types (the Go side owns the rules).
  React.useEffect(() => {
    if (!open || (!url && !name)) {
      setValidation(null)
      return
    }
    let stale = false
    const timer = setTimeout(async () => {
      try {
        const v = await backend.validate(name, url)
        if (!stale) setValidation(v)
      } catch {
        // Validation is a convenience; the save checks again.
      }
    }, 250)
    return () => {
      stale = true
      clearTimeout(timer)
    }
  }, [open, name, url])

  React.useEffect(
    () =>
      backend.onSignInProgress((p) => {
        if (p.attempt && p.attempt === attempt.current) setProgress(p)
      }),
    [],
  )

  // Secrets live only in this form: drop them as soon as they are not needed.
  const clearSecrets = () => {
    setAPISecret("")
    setPassword("")
    setClientSecret("")
  }

  function close() {
    if (busy && method === "oauth") void backend.cancelSignIn()
    clearSecrets()
    onOpenChange(false)
  }

  const nameError =
    validation?.nameError ||
    (validation?.exists && !replace ? t("addSite.nameExists", { name: validation.name }) : "")
  const urlError = validation?.urlError ?? ""
  const step1OK = !!validation && validation.ok && validation.name === name.trim() && (!validation.exists || replace)

  function onURLChange(v: string) {
    setURL(v)
    if (!nameTouched) setName(suggestName(v))
  }

  function next1(e: React.FormEvent) {
    e.preventDefault()
    setShowErrors(true)
    if (step1OK) setStep(2)
  }

  function finish(site: AddedSite) {
    clearSecrets()
    setAdded(site)
    setIsDefault(site.isDefault)
    setStep(4)
    void reloadSites()
    toast.add({
      title: site.replaced ? t("addSite.toast.updated", { name: site.name }) : t("addSite.toast.added", { name: site.name }),
      description: site.user ? t("addSite.signedInAs", { user: site.user }) : undefined,
      type: "success",
    })
  }

  async function signIn() {
    const mine = session.current
    const current = () => mine === session.current
    setBusy(true)
    setError(null)
    setProgress(null)
    try {
      let site: AddedSite
      const base = { name, url: validation?.url || url, replace }
      if (method === "oauth") {
        attempt.current = `${mine}-${Date.now()}`
        site = await backend.signInWithBrowser({
          ...base,
          clientID: advanced ? clientID.trim() : "",
          clientSecret: advanced && clientID.trim() ? clientSecret : "",
          attempt: attempt.current,
        })
      } else if (method === "apikey") {
        site = await backend.addWithAPIKey({ ...base, apiKey: apiKey.trim(), apiSecret })
      } else {
        site = await backend.addWithPassword({ ...base, username: username.trim(), password })
      }
      if (current()) finish(site)
    } catch (err) {
      if (!current()) return
      const e = appError(err)
      if (e.code === "cancelled") {
        setStep(2)
      } else if (e.code === "invalid" && (e.field === "name" || e.field === "url")) {
        setStep(1)
        setShowErrors(true)
        setError(e)
      } else if (e.code === "exists") {
        setStep(1)
        setShowErrors(true)
      } else {
        setError(e)
      }
    } finally {
      if (current()) setBusy(false)
    }
  }

  async function reopenSignInPage() {
    try {
      await backend.reopenSignInPage()
    } catch (err) {
      const e = appError(err)
      toast.add({ title: t("addSite.toast.openFailed"), description: e.message, type: "error" })
    }
  }

  // Browser sign-in starts as soon as its step opens.
  React.useEffect(() => {
    if (open && step === 3 && method === "oauth" && !busy && !error && !added) void signIn()
    // signIn reads the current form; it must run once per entry into step 3.
  }, [open, step])

  async function cancelOAuth() {
    await backend.cancelSignIn()
  }

  async function toggleDefault(on: boolean) {
    if (!added || !on) return
    try {
      await backend.setDefault(added.name)
      setIsDefault(true)
      void reloadSites()
      toast.add({ title: t("sites.toast.nowDefault", { name: added.name }), type: "success" })
    } catch (err) {
      const e = appError(err)
      toast.add({ title: t("addSite.toast.defaultFailed"), description: e.message, type: "error" })
    }
  }

  const onlySite = (sites.data?.sites?.length ?? 0) <= 1

  return (
    <Sheet open={open} onOpenChange={(o) => (o ? onOpenChange(true) : close())}>
      <SheetContent className="w-full gap-0 data-[side=right]:sm:max-w-md">
        <SheetHeader className="border-b">
          <SheetTitle>{t("sites.add")}</SheetTitle>
          <SheetDescription>{t("addSite.description")}</SheetDescription>
          <Progress value={(step / 4) * 100} className="pt-2">
            <ProgressLabel>
              {t("addSite.stepOf", { step, title: stepTitle(t, step) })}
            </ProgressLabel>
            <ProgressValue className="sr-only" />
          </Progress>
        </SheetHeader>

        <div className="flex-1 overflow-y-auto p-4">
          {step === 1 && (
            <form id="site-form" onSubmit={next1}>
              <FieldGroup>
                <Field data-invalid={(showErrors && !!urlError) || undefined}>
                  <FieldLabel htmlFor="site-url">{t("addSite.url.label")}</FieldLabel>
                  <Input
                    id="site-url"
                    value={url}
                    onChange={(e) => onURLChange(e.target.value)}
                    placeholder="erp.example.com"
                    autoComplete="url"
                    spellCheck={false}
                    autoFocus
                    aria-invalid={(showErrors && !!urlError) || undefined}
                  />
                  {showErrors && urlError ? (
                    <FieldError>{urlError}</FieldError>
                  ) : (
                    <FieldDescription>{t("addSite.url.hint")}</FieldDescription>
                  )}
                </Field>
                <Field data-invalid={(showErrors && !!nameError) || undefined}>
                  <FieldLabel htmlFor="site-name">{t("addSite.name.label")}</FieldLabel>
                  <Input
                    id="site-name"
                    value={name}
                    onChange={(e) => {
                      setNameTouched(true)
                      setName(e.target.value)
                    }}
                    placeholder="acme-prod"
                    autoComplete="off"
                    spellCheck={false}
                    aria-invalid={(showErrors && !!nameError) || undefined}
                  />
                  {showErrors && nameError ? (
                    <FieldError>{nameError}</FieldError>
                  ) : (
                    <FieldDescription>{t("addSite.name.hint")}</FieldDescription>
                  )}
                </Field>
                {validation?.exists && (
                  <Field orientation="horizontal">
                    <Checkbox id="site-replace" checked={replace} onCheckedChange={(v) => setReplace(v === true)} />
                    <FieldContent>
                      <FieldLabel htmlFor="site-replace">{t("addSite.replace.label", { name: validation.name })}</FieldLabel>
                      <FieldDescription>{t("addSite.replace.hint")}</FieldDescription>
                    </FieldContent>
                  </Field>
                )}
                {validation?.plainHTTP && !urlError && (
                  <Alert variant="destructive">
                    <IconLockOpen />
                    <AlertTitle>{t("addSite.http.title")}</AlertTitle>
                    <AlertDescription>
                      {t("addSite.http.body")}
                    </AlertDescription>
                  </Alert>
                )}
                {error && step === 1 && error.code === "invalid" && (
                  <Alert variant="destructive">
                    <IconAlertTriangle />
                    <AlertTitle>{t("addSite.checkDetails")}</AlertTitle>
                    <AlertDescription>{error.message}</AlertDescription>
                  </Alert>
                )}
              </FieldGroup>
            </form>
          )}

          {step === 2 && (
            <FieldGroup>
              <FieldSet>
                <FieldLegend variant="label">{t("addSite.method.legend", { name })}</FieldLegend>
                <RadioGroup value={method} onValueChange={(v) => setMethod(v as Method)}>
                  <MethodCard
                    value="oauth"
                    icon={<IconBrowser />}
                    title={t("addSite.method.oauth.title")}
                    badge={t("addSite.method.recommended")}
                    description={t("addSite.method.oauth.description")}
                  />
                  <MethodCard
                    value="apikey"
                    icon={<IconKey />}
                    title={t("addSite.method.apikey.title")}
                    description={t("addSite.method.apikey.description")}
                  />
                  <MethodCard
                    value="password"
                    icon={<IconUser />}
                    title={t("addSite.method.password.title")}
                    description={t("addSite.method.password.description")}
                  />
                </RadioGroup>
              </FieldSet>
              {method === "oauth" && (
                <Collapsible open={advanced} onOpenChange={setAdvanced}>
                  <CollapsibleTrigger
                    render={<Button variant="ghost" size="sm" className="text-muted-foreground -ml-2" />}
                  >
                    <IconChevronDown
                      data-icon="inline-start"
                      className="transition-transform in-aria-expanded:rotate-180"
                    />
                    {t("addSite.advanced.toggle")}
                  </CollapsibleTrigger>
                  <CollapsibleContent>
                    <FieldGroup className="pt-3">
                      <FieldDescription>
                        {t("addSite.advanced.hint")}
                      </FieldDescription>
                      <Field>
                        <FieldLabel htmlFor="client-id">{t("addSite.advanced.clientId")}</FieldLabel>
                        <Input
                          id="client-id"
                          value={clientID}
                          onChange={(e) => setClientID(e.target.value)}
                          autoComplete="off"
                          spellCheck={false}
                          className="font-mono"
                        />
                      </Field>
                      <Field>
                        <FieldLabel htmlFor="client-secret">
                          {t("addSite.advanced.clientSecret")}{" "}
                          <span className="text-muted-foreground font-normal">{t("addSite.optional")}</span>
                        </FieldLabel>
                        <SecretInput id="client-secret" value={clientSecret} onChange={setClientSecret} />
                        <FieldDescription>
                          {t("addSite.advanced.secretHint")}
                        </FieldDescription>
                      </Field>
                    </FieldGroup>
                  </CollapsibleContent>
                </Collapsible>
              )}
            </FieldGroup>
          )}

          {step === 3 && method === "oauth" && (
            <OAuthStep
              busy={busy}
              progress={progress}
              error={error}
              onRetry={() => void signIn()}
              onUseClient={() => {
                setError(null)
                setAdvanced(true)
                setStep(2)
              }}
            />
          )}

          {step === 3 && method !== "oauth" && (
            <form id="secret-form" onSubmit={(e) => (e.preventDefault(), void signIn())}>
              <FieldGroup>
                {method === "apikey" ? (
                  <>
                    <FieldDescription>
                      {t("addSite.apikey.help")}
                    </FieldDescription>
                    <Field data-invalid={error?.field === "apiKey" || undefined}>
                      <FieldLabel htmlFor="api-key">{t("addSite.apikey.key")}</FieldLabel>
                      <Input
                        id="api-key"
                        value={apiKey}
                        onChange={(e) => setAPIKey(e.target.value)}
                        autoComplete="off"
                        spellCheck={false}
                        className="font-mono"
                        autoFocus
                        aria-invalid={error?.field === "apiKey" || undefined}
                      />
                    </Field>
                    <Field data-invalid={error?.field === "apiSecret" || undefined}>
                      <FieldLabel htmlFor="api-secret">{t("addSite.apikey.secret")}</FieldLabel>
                      <SecretInput
                        id="api-secret"
                        value={apiSecret}
                        onChange={setAPISecret}
                        invalid={error?.field === "apiSecret"}
                      />
                    </Field>
                  </>
                ) : (
                  <>
                    <Field data-invalid={error?.field === "username" || undefined}>
                      <FieldLabel htmlFor="username">{t("addSite.password.username")}</FieldLabel>
                      <Input
                        id="username"
                        value={username}
                        onChange={(e) => setUsername(e.target.value)}
                        autoComplete="username"
                        spellCheck={false}
                        autoFocus
                        aria-invalid={error?.field === "username" || undefined}
                      />
                    </Field>
                    <Field data-invalid={error?.field === "password" || undefined}>
                      <FieldLabel htmlFor="password">{t("addSite.password.password")}</FieldLabel>
                      <SecretInput
                        id="password"
                        value={password}
                        onChange={setPassword}
                        autoComplete="current-password"
                        invalid={error?.field === "password"}
                      />
                      <FieldDescription>
                        {t("addSite.password.twoFactor")}
                      </FieldDescription>
                    </Field>
                  </>
                )}
                {error && (
                  <Alert variant="destructive">
                    <IconAlertTriangle />
                    <AlertTitle>
                      {error.code === "auth"
                        ? t("addSite.error.rejected")
                        : error.code === "network"
                          ? t("addSite.error.network")
                          : t("addSite.error.failed")}
                    </AlertTitle>
                    <AlertDescription>
                      <p>{error.message}</p>
                      {error.detail && <Details>{error.detail}</Details>}
                    </AlertDescription>
                  </Alert>
                )}
              </FieldGroup>
            </form>
          )}

          {step === 4 && added && (
            <div className="flex flex-col gap-6">
              <Alert>
                <IconCircleCheck />
                <AlertTitle>{t("addSite.ready", { name: added.name })}</AlertTitle>
                <AlertDescription>
                  {added.user ? t("addSite.signedInAs", { user: added.user }) : ""}
                  {added.user && added.registered ? " " : ""}
                  {added.registered ? t("addSite.registered") : ""}
                </AlertDescription>
              </Alert>
              <Field orientation="horizontal">
                <FieldContent>
                  <FieldLabel htmlFor="make-default">{t("addSite.default.label")}</FieldLabel>
                  <FieldDescription>
                    {isDefault && onlySite
                      ? t("addSite.default.only")
                      : t("addSite.default.hint")}
                  </FieldDescription>
                </FieldContent>
                <Switch id="make-default" checked={isDefault} disabled={isDefault} onCheckedChange={toggleDefault} />
              </Field>
            </div>
          )}
        </div>

        <SheetFooter className="border-t sm:flex-row sm:justify-between">
          {step === 1 && (
            <>
              <Button variant="outline" onClick={close}>
                {t("common.cancel")}
              </Button>
              <Button type="submit" form="site-form" disabled={showErrors && !step1OK}>
                {t("common.continue")}
                <IconArrowRight data-icon="inline-end" />
              </Button>
            </>
          )}
          {step === 2 && (
            <>
              <Button variant="outline" onClick={() => setStep(1)}>
                <IconArrowLeft data-icon="inline-start" />
                {t("common.back")}
              </Button>
              <Button
                onClick={() => {
                  setError(null)
                  setStep(3)
                }}
                disabled={method === "oauth" && advanced && !clientID.trim() && !!clientSecret}
              >
                {method === "oauth" ? t("addSite.openSignIn") : t("common.continue")}
                <IconArrowRight data-icon="inline-end" />
              </Button>
            </>
          )}
          {step === 3 && method === "oauth" && (
            <>
              <Button variant="outline" onClick={() => (busy ? void cancelOAuth() : setStep(2))}>
                {busy ? (
                  t("addSite.cancelSignIn")
                ) : (
                  <>
                    <IconArrowLeft data-icon="inline-start" />
                    {t("common.back")}
                  </>
                )}
              </Button>
              {busy && progress?.step === "browser" && (
                <Button variant="secondary" onClick={() => void reopenSignInPage()}>
                  <IconExternalLink data-icon="inline-start" />
                  {t("addSite.openAgain")}
                </Button>
              )}
            </>
          )}
          {step === 3 && method !== "oauth" && (
            <>
              <Button variant="outline" onClick={() => setStep(2)} disabled={busy}>
                <IconArrowLeft data-icon="inline-start" />
                {t("common.back")}
              </Button>
              <Button
                type="submit"
                form="secret-form"
                disabled={busy || (method === "apikey" ? !apiKey.trim() || !apiSecret : !username.trim() || !password)}
              >
                {busy && <Spinner data-icon="inline-start" />}
                {busy ? t("sites.checking") : method === "apikey" ? t("addSite.checkAndSave") : t("addSite.signIn")}
              </Button>
            </>
          )}
          {step === 4 && (
            <>
              <Button variant="outline" onClick={close}>
                {t("addSite.done")}
              </Button>
              <Button
                onClick={() => {
                  const site = added?.name
                  close()
                  connectAssistant(undefined, site)
                }}
              >
                <IconPlugConnected data-icon="inline-start" />
                {t("sites.action.connect")}
              </Button>
            </>
          )}
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}

function MethodCard({
  value,
  icon,
  title,
  description,
  badge,
}: {
  value: Method
  icon: React.ReactNode
  title: string
  description: string
  badge?: string
}) {
  const id = `method-${value}`
  return (
    <FieldLabel htmlFor={id}>
      <Field orientation="horizontal">
        <span className="text-muted-foreground [&_svg:not([class*='size-'])]:size-5">{icon}</span>
        <FieldContent>
          <FieldTitle>
            {title}
            {badge && <Badge variant="secondary">{badge}</Badge>}
          </FieldTitle>
          <FieldDescription>{description}</FieldDescription>
        </FieldContent>
        <RadioGroupItem value={value} id={id} />
      </Field>
    </FieldLabel>
  )
}

/** Go's progress message in the page's language, by step (the English text is Go's own). */
// i18n keys: addSite.progress.starting addSite.progress.registering addSite.progress.browser addSite.progress.saving addSite.progress.done
function progressText(t: TFunction, p: SignInProgress) {
  switch (p.step) {
    case "starting":
    case "registering":
    case "browser":
    case "saving":
    case "done":
      return t(`addSite.progress.${p.step}`, { defaultValue: p.message })
    case "finishing":
      if (p.message === "Completing the sign-in…") return t("addSite.progress.finishingExchange")
      if (p.message === "Checking who signed in…") return t("addSite.progress.finishingUser")
      return t("addSite.progress.finishing", { defaultValue: p.message })
    default:
      return p.message
  }
}

function OAuthStep({
  busy,
  progress,
  error,
  onRetry,
  onUseClient,
}: {
  busy: boolean
  progress: SignInProgress | null
  error: AppError | null
  onRetry: () => void
  onUseClient: () => void
}) {
  const { t } = useTranslation()
  if (error?.code === "no_registration") {
    return (
      <div className="flex flex-col gap-4">
        <Alert>
          <IconAlertTriangle />
          <AlertTitle>{t("addSite.oauth.noReg.title")}</AlertTitle>
          <AlertDescription>
            {error.unsupported
              ? t("addSite.oauth.noReg.unsupported")
              : t("addSite.oauth.noReg.refused")}
          </AlertDescription>
        </Alert>
        <ol className="text-muted-foreground flex list-decimal flex-col gap-3 pl-5 text-sm">
          <li>
            <Trans
              i18nKey="addSite.oauth.manual.step1"
              components={{ b: <span className="text-foreground font-medium" /> }}
            />
          </li>
          <li className="flex flex-col gap-2">
            <span>
              <Trans
                i18nKey="addSite.oauth.manual.step2"
                components={{ b: <span className="text-foreground font-medium" /> }}
              />
            </span>
            {error.redirectURI && (
              <CopyField value={error.redirectURI} label={t("addSite.oauth.redirectUri")} copiedTitle={t("addSite.oauth.redirectUriCopied")} />
            )}
          </li>
          <li>{t("addSite.oauth.manual.step3")}</li>
        </ol>
        {error.detail && <Details>{error.detail}</Details>}
        <Button onClick={onUseClient} className="self-start">
          {t("addSite.oauth.enterClientId")}
        </Button>
      </div>
    )
  }

  if (error) {
    return (
      <div className="flex flex-col gap-4">
        <Alert variant="destructive">
          <IconAlertTriangle />
          <AlertTitle>
            {error.code === "auth"
              ? t("addSite.oauth.error.auth")
              : error.code === "network"
                ? t("addSite.error.network")
                : t("addSite.oauth.error.failed")}
          </AlertTitle>
          <AlertDescription>
            <p>{error.message}</p>
            {error.detail && <Details>{error.detail}</Details>}
          </AlertDescription>
        </Alert>
        <Button onClick={onRetry} className="self-start">
          {t("common.retry")}
        </Button>
      </div>
    )
  }

  const step = progress?.step ?? "starting"
  // i18n keys: addSite.progress.starting addSite.progress.registering addSite.progress.browser addSite.progress.finishing addSite.progress.finishingExchange addSite.progress.finishingUser addSite.progress.saving addSite.progress.done
  const message = progress ? progressText(t, progress) : t("addSite.progress.starting")
  return (
    <div className="flex flex-col items-center gap-4 py-6 text-center" aria-live="polite">
      {busy ? <Spinner className="size-8" /> : <IconBrowser className="text-muted-foreground size-8" />}
      <div className="flex flex-col gap-1">
        <p className="font-medium">{step === "browser" ? t("addSite.oauth.waiting") : t("addSite.oauth.preparing")}</p>
        <p className="text-muted-foreground text-sm">
          {message}
          {step === "browser" && ` ${t("addSite.oauth.comeBack")}`}
        </p>
      </div>
      {progress?.browserError && (
        <Alert className="text-left">
          <IconAlertTriangle />
          <AlertTitle>{t("addSite.oauth.browserFailed")}</AlertTitle>
          <AlertDescription>{t("addSite.oauth.copyLinkHint")}</AlertDescription>
        </Alert>
      )}
      {progress?.authURL && step === "browser" && (
        <div className="flex w-full flex-col gap-2 text-left">
          {progress.browserError ? (
            <CopyField value={progress.authURL} label={t("addSite.oauth.link")} copiedTitle={t("addSite.oauth.linkCopied")} />
          ) : (
            <Button
              variant="link"
              size="sm"
              className="self-center"
              onClick={() => void copy(progress.authURL!, t("addSite.oauth.linkCopied"))}
            >
              {t("addSite.oauth.copyLink")}
            </Button>
          )}
        </div>
      )}
    </div>
  )
}
