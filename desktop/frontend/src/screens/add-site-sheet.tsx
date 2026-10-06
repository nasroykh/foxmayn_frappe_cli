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

const stepTitles: Record<Step, string> = {
  1: "Your site",
  2: "How to sign in",
  3: "Sign in",
  4: "Done",
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
  const [isDefault, setIsDefault] = React.useState(false)

  // Start fresh each time the sheet opens.
  React.useEffect(() => {
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
    const t = setTimeout(async () => {
      try {
        const v = await backend.validate(name, url)
        if (!stale) setValidation(v)
      } catch {
        // Validation is a convenience; the save checks again.
      }
    }, 250)
    return () => {
      stale = true
      clearTimeout(t)
    }
  }, [open, name, url])

  React.useEffect(() => backend.onSignInProgress(setProgress), [])

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
    (validation?.exists && !replace ? `A site called "${validation.name}" already exists.` : "")
  const urlError = validation?.urlError ?? ""
  const step1OK = !!validation && validation.ok && validation.name === name && (!validation.exists || replace)

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
      title: site.replaced ? `${site.name} was updated` : `${site.name} was added`,
      description: site.user ? `Signed in as ${site.user}.` : undefined,
      type: "success",
    })
  }

  async function signIn() {
    setBusy(true)
    setError(null)
    setProgress(null)
    try {
      let site: AddedSite
      const base = { name, url: validation?.url || url, replace }
      if (method === "oauth") {
        site = await backend.signInWithBrowser({
          ...base,
          clientID: advanced ? clientID.trim() : "",
          clientSecret: advanced && clientID.trim() ? clientSecret : "",
        })
      } else if (method === "apikey") {
        site = await backend.addWithAPIKey({ ...base, apiKey: apiKey.trim(), apiSecret })
      } else {
        site = await backend.addWithPassword({ ...base, username: username.trim(), password })
      }
      finish(site)
    } catch (err) {
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
      setBusy(false)
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
      toast.add({ title: `${added.name} is now the default site`, type: "success" })
    } catch (err) {
      const e = appError(err)
      toast.add({ title: "Could not change the default site", description: e.message, type: "error" })
    }
  }

  const onlySite = (sites.data?.sites?.length ?? 0) <= 1

  return (
    <Sheet open={open} onOpenChange={(o) => (o ? onOpenChange(true) : close())}>
      <SheetContent className="w-full gap-0 sm:max-w-md">
        <SheetHeader className="border-b">
          <SheetTitle>Add a site</SheetTitle>
          <SheetDescription>Connect a Frappe or ERPNext site to this computer.</SheetDescription>
          <Progress value={(step / 4) * 100} className="pt-2">
            <ProgressLabel>
              Step {step} of 4: {stepTitles[step]}
            </ProgressLabel>
            <ProgressValue className="sr-only" />
          </Progress>
        </SheetHeader>

        <div className="flex-1 overflow-y-auto p-4">
          {step === 1 && (
            <form id="site-form" onSubmit={next1}>
              <FieldGroup>
                <Field data-invalid={(showErrors && !!urlError) || undefined}>
                  <FieldLabel htmlFor="site-url">Site address</FieldLabel>
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
                    <FieldDescription>The address you open in your browser to use the site.</FieldDescription>
                  )}
                </Field>
                <Field data-invalid={(showErrors && !!nameError) || undefined}>
                  <FieldLabel htmlFor="site-name">Name</FieldLabel>
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
                    <FieldDescription>A short name to tell your sites apart, like acme-prod.</FieldDescription>
                  )}
                </Field>
                {validation?.exists && (
                  <Field orientation="horizontal">
                    <Checkbox id="site-replace" checked={replace} onCheckedChange={(v) => setReplace(v === true)} />
                    <FieldContent>
                      <FieldLabel htmlFor="site-replace">Replace the saved site called {validation.name}</FieldLabel>
                      <FieldDescription>Its address and sign-in are overwritten when you finish.</FieldDescription>
                    </FieldContent>
                  </Field>
                )}
                {validation?.plainHTTP && !urlError && (
                  <Alert variant="destructive">
                    <IconLockOpen />
                    <AlertTitle>This address is not encrypted</AlertTitle>
                    <AlertDescription>
                      It starts with http://, so your sign-in details travel in the clear. Use it only for a site on
                      this computer or your own network.
                    </AlertDescription>
                  </Alert>
                )}
                {error && step === 1 && error.code === "invalid" && (
                  <Alert variant="destructive">
                    <IconAlertTriangle />
                    <AlertTitle>Check the details</AlertTitle>
                    <AlertDescription>{error.message}</AlertDescription>
                  </Alert>
                )}
              </FieldGroup>
            </form>
          )}

          {step === 2 && (
            <FieldGroup>
              <FieldSet>
                <FieldLegend variant="label">Choose how to sign in to {name}</FieldLegend>
                <RadioGroup value={method} onValueChange={(v) => setMethod(v as Method)}>
                  <MethodCard
                    value="oauth"
                    icon={<IconBrowser />}
                    title="Sign in with your browser"
                    badge="Recommended"
                    description="Sign in on your site's own page. This app never sees your password, and it works with two-factor sign-in."
                  />
                  <MethodCard
                    value="apikey"
                    icon={<IconKey />}
                    title="Use an API key"
                    description="Paste an API key and secret from your Frappe user settings. Good for integration users."
                  />
                  <MethodCard
                    value="password"
                    icon={<IconUser />}
                    title="Use your username and password"
                    description="Saved on this computer. Accounts with two-factor sign-in must use browser sign-in or an API key instead."
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
                    Advanced: use your own OAuth client
                  </CollapsibleTrigger>
                  <CollapsibleContent>
                    <FieldGroup className="pt-3">
                      <FieldDescription>
                        Only needed when your site cannot set up the app by itself, for example on Frappe v15. Create an
                        OAuth Client on your site and paste its details here.
                      </FieldDescription>
                      <Field>
                        <FieldLabel htmlFor="client-id">Client ID</FieldLabel>
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
                          Client secret <span className="text-muted-foreground font-normal">(optional)</span>
                        </FieldLabel>
                        <SecretInput id="client-secret" value={clientSecret} onChange={setClientSecret} />
                        <FieldDescription>
                          Leave it empty for a public client. It is stored with the site, never shown again.
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
                      In Frappe, open your user menu, choose My Settings, then API Access, and generate keys. The secret
                      is shown only once there.
                    </FieldDescription>
                    <Field data-invalid={error?.field === "apiKey" || undefined}>
                      <FieldLabel htmlFor="api-key">API key</FieldLabel>
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
                      <FieldLabel htmlFor="api-secret">API secret</FieldLabel>
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
                      <FieldLabel htmlFor="username">Username or email</FieldLabel>
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
                      <FieldLabel htmlFor="password">Password</FieldLabel>
                      <SecretInput
                        id="password"
                        value={password}
                        onChange={setPassword}
                        autoComplete="current-password"
                        invalid={error?.field === "password"}
                      />
                      <FieldDescription>
                        Two-factor accounts cannot sign in this way. Go back and choose browser sign-in or an API key.
                      </FieldDescription>
                    </Field>
                  </>
                )}
                {error && (
                  <Alert variant="destructive">
                    <IconAlertTriangle />
                    <AlertTitle>
                      {error.code === "auth"
                        ? "The site did not accept these details"
                        : error.code === "network"
                          ? "Could not reach the site"
                          : "That did not work"}
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
                <AlertTitle>{added.name} is ready</AlertTitle>
                <AlertDescription>
                  {added.user ? `Signed in as ${added.user}. ` : ""}
                  {added.registered ? "The app was set up on your site for you." : ""}
                </AlertDescription>
              </Alert>
              <Field orientation="horizontal">
                <FieldContent>
                  <FieldLabel htmlFor="make-default">Use as the default site</FieldLabel>
                  <FieldDescription>
                    {isDefault && onlySite
                      ? "It is your only site, so it is the default."
                      : "Assistants that follow the default site use this one."}
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
                Cancel
              </Button>
              <Button type="submit" form="site-form" disabled={showErrors && !step1OK}>
                Continue
                <IconArrowRight data-icon="inline-end" />
              </Button>
            </>
          )}
          {step === 2 && (
            <>
              <Button variant="outline" onClick={() => setStep(1)}>
                <IconArrowLeft data-icon="inline-start" />
                Back
              </Button>
              <Button
                onClick={() => {
                  setError(null)
                  setStep(3)
                }}
                disabled={method === "oauth" && advanced && !clientID.trim() && !!clientSecret}
              >
                {method === "oauth" ? "Open the sign-in page" : "Continue"}
                <IconArrowRight data-icon="inline-end" />
              </Button>
            </>
          )}
          {step === 3 && method === "oauth" && (
            <>
              <Button variant="outline" onClick={() => (busy ? void cancelOAuth() : setStep(2))}>
                {busy ? (
                  "Cancel sign-in"
                ) : (
                  <>
                    <IconArrowLeft data-icon="inline-start" />
                    Back
                  </>
                )}
              </Button>
              {busy && progress?.step === "browser" && (
                <Button variant="secondary" onClick={() => void backend.reopenSignInPage()}>
                  <IconExternalLink data-icon="inline-start" />
                  Open the page again
                </Button>
              )}
            </>
          )}
          {step === 3 && method !== "oauth" && (
            <>
              <Button variant="outline" onClick={() => setStep(2)} disabled={busy}>
                <IconArrowLeft data-icon="inline-start" />
                Back
              </Button>
              <Button
                type="submit"
                form="secret-form"
                disabled={busy || (method === "apikey" ? !apiKey.trim() || !apiSecret : !username.trim() || !password)}
              >
                {busy && <Spinner data-icon="inline-start" />}
                {busy ? "Checking…" : method === "apikey" ? "Check and save" : "Sign in"}
              </Button>
            </>
          )}
          {step === 4 && (
            <>
              <Button variant="outline" onClick={close}>
                Done
              </Button>
              <Button
                onClick={() => {
                  const site = added?.name
                  close()
                  connectAssistant(undefined, site)
                }}
              >
                <IconPlugConnected data-icon="inline-start" />
                Connect an assistant
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
  if (error?.code === "no_registration") {
    return (
      <div className="flex flex-col gap-4">
        <Alert>
          <IconAlertTriangle />
          <AlertTitle>This site cannot set up the app by itself</AlertTitle>
          <AlertDescription>
            {error.unsupported
              ? "Older Frappe versions, such as v15, do not let apps register themselves. An administrator can create the app on the site once, then you sign in as usual."
              : "The site refused to set up the app. An administrator can create it by hand instead."}
          </AlertDescription>
        </Alert>
        <ol className="text-muted-foreground flex list-decimal flex-col gap-3 pl-5 text-sm">
          <li>
            On your site, open <span className="text-foreground font-medium">OAuth Client</span> and create a new one.
          </li>
          <li className="flex flex-col gap-2">
            <span>
              Set the grant type to <span className="text-foreground font-medium">Authorization Code</span>, the
              response type to <span className="text-foreground font-medium">Code</span>, and this redirect URI:
            </span>
            {error.redirectURI && (
              <CopyField value={error.redirectURI} label="Redirect URI" copiedTitle="Redirect URI copied" />
            )}
          </li>
          <li>Save it, then paste its client ID (and secret, if it has one) here.</li>
        </ol>
        {error.detail && <Details>{error.detail}</Details>}
        <Button onClick={onUseClient} className="self-start">
          Enter the client ID
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
              ? "The sign-in was not completed"
              : error.code === "network"
                ? "Could not reach the site"
                : "Sign-in did not work"}
          </AlertTitle>
          <AlertDescription>
            <p>{error.message}</p>
            {error.detail && <Details>{error.detail}</Details>}
          </AlertDescription>
        </Alert>
        <Button onClick={onRetry} className="self-start">
          Try again
        </Button>
      </div>
    )
  }

  const step = progress?.step ?? "starting"
  return (
    <div className="flex flex-col items-center gap-4 py-6 text-center" aria-live="polite">
      {busy ? <Spinner className="size-8" /> : <IconBrowser className="text-muted-foreground size-8" />}
      <div className="flex flex-col gap-1">
        <p className="font-medium">{step === "browser" ? "Waiting for your browser" : "Getting things ready"}</p>
        <p className="text-muted-foreground text-sm">
          {progress?.message ?? "Getting ready…"}
          {step === "browser" && " When you approve the app there, come back here."}
        </p>
      </div>
      {progress?.browserError && (
        <Alert className="text-left">
          <IconAlertTriangle />
          <AlertTitle>Your browser did not open</AlertTitle>
          <AlertDescription>Copy this link into your browser to continue.</AlertDescription>
        </Alert>
      )}
      {progress?.authURL && step === "browser" && (
        <div className="flex w-full flex-col gap-2 text-left">
          {progress.browserError ? (
            <CopyField value={progress.authURL} label="Sign-in link" copiedTitle="Link copied" />
          ) : (
            <Button
              variant="link"
              size="sm"
              className="self-center"
              onClick={() => void copy(progress.authURL!, "Link copied")}
            >
              Copy the sign-in link instead
            </Button>
          )}
        </div>
      )}
    </div>
  )
}
