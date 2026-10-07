import {
  IconAlertTriangle,
  IconArrowUpCircle,
  IconBrandGithub,
  IconBug,
  IconCircleCheck,
  IconDeviceDesktop,
  IconDownload,
  IconFolderOpen,
  IconMoon,
  IconRefresh,
  IconSun,
} from "@tabler/icons-react"
import * as React from "react"

import { useApp } from "@/app/app-context"
import { useTheme, type Theme } from "@/app/theme"
import { BrandLogo } from "@/components/brand-logo"
import { CopyField } from "@/components/copy-field"
import { PageHeader } from "@/components/page"
import { Alert, AlertAction, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card"
import { Field, FieldContent, FieldDescription, FieldGroup, FieldLabel, FieldTitle } from "@/components/ui/field"
import { Separator } from "@/components/ui/separator"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { toast } from "@/components/ui/toast"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { backend } from "@/lib/backend"
import type { UpdateInfo } from "@/lib/backend-types"
import { appError, errorTitle, type AppError } from "@/lib/errors"
import { WSLAlert } from "@/screens/wsl-alert"

const REPO = "https://github.com/nasroykh/foxmayn_frappe_cli"

async function attempt(fn: () => Promise<unknown>) {
  try {
    await fn()
  } catch (err) {
    const e = appError(err)
    toast.add({ title: errorTitle(e), description: e.message, type: "error" })
  }
}

export function SettingsScreen() {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader title="Settings" description="How the app looks, where things are kept, and what it is." />
      <Tabs defaultValue="general">
        <TabsList>
          <TabsTrigger value="general">General</TabsTrigger>
          <TabsTrigger value="ffc">ffc helper</TabsTrigger>
          <TabsTrigger value="about">About</TabsTrigger>
        </TabsList>
        <TabsContent value="general" className="pt-4">
          <GeneralTab />
        </TabsContent>
        <TabsContent value="ffc" className="pt-4">
          <FFCTab />
        </TabsContent>
        <TabsContent value="about" className="pt-4">
          <AboutTab />
        </TabsContent>
      </Tabs>
    </div>
  )
}

function GeneralTab() {
  const { theme, setTheme } = useTheme()
  const { env } = useApp()
  return (
    <div className="flex max-w-2xl flex-col gap-6">
      <FieldGroup>
        <Field orientation="horizontal">
          <FieldContent>
            <FieldTitle>Theme</FieldTitle>
            <FieldDescription>Light, dark, or the same as your system.</FieldDescription>
          </FieldContent>
          <ToggleGroup
            variant="outline"
            value={[theme]}
            onValueChange={(v: string[]) => v[0] && setTheme(v[0] as Theme)}
            aria-label="Theme"
          >
            <ToggleGroupItem value="light" aria-label="Light">
              <IconSun data-icon="inline-start" />
              Light
            </ToggleGroupItem>
            <ToggleGroupItem value="dark" aria-label="Dark">
              <IconMoon data-icon="inline-start" />
              Dark
            </ToggleGroupItem>
            <ToggleGroupItem value="system" aria-label="Same as system">
              <IconDeviceDesktop data-icon="inline-start" />
              System
            </ToggleGroupItem>
          </ToggleGroup>
        </Field>
        <Separator />
        <Field>
          <FieldLabel htmlFor="config-path">Settings file</FieldLabel>
          {env.data ? (
            <div className="flex gap-2">
              <div className="min-w-0 flex-1">
                <CopyField value={env.data.configPath} label="Settings file path" copiedTitle="Path copied" />
              </div>
              <Button
                variant="outline"
                onClick={() => attempt(backend.openConfigFolder)}
                disabled={!env.data.configExists}
              >
                <IconFolderOpen data-icon="inline-start" />
                Open folder
              </Button>
            </div>
          ) : (
            <Skeleton className="h-8 w-full" />
          )}
          <FieldDescription>
            {env.data && !env.data.configExists
              ? "It does not exist yet. It is created when you add your first site."
              : "Shared with the ffc command line. Changes made there show up here right away."}
          </FieldDescription>
        </Field>
      </FieldGroup>
      <WSLAlert />
    </div>
  )
}

function FFCTab() {
  const { env, reloadEnv, reloadAssistants, installFFC, ffcUpdate } = useApp()
  const [refreshing, setRefreshing] = React.useState(false)
  const ffc = env.data?.ffc

  async function refresh() {
    setRefreshing(true)
    try {
      await backend.refreshFFC()
      await Promise.all([reloadEnv(), reloadAssistants()])
    } catch (err) {
      const e = appError(err)
      toast.add({ title: errorTitle(e), description: e.message, type: "error" })
    } finally {
      setRefreshing(false)
    }
  }

  if (!env.data || !ffc) return <Skeleton className="h-48 max-w-2xl rounded-xl" />

  const ok = ffc.found && !ffc.error
  return (
    <div className="flex max-w-2xl flex-col gap-6">
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            ffc helper
            {ok && ffcUpdate ? (
              <Badge variant="secondary">
                <IconArrowUpCircle data-icon="inline-start" />
                Update available
              </Badge>
            ) : ok ? (
              <Badge>
                <IconCircleCheck data-icon="inline-start" />
                Installed
              </Badge>
            ) : (
              <Badge variant="destructive">
                <IconAlertTriangle data-icon="inline-start" />
                {ffc.found ? "Not working" : "Not installed"}
              </Badge>
            )}
          </CardTitle>
          <CardDescription>
            The program assistants run to reach your sites. This app finds it on your PATH or where the ffc
            installers put it, and can install the latest release for you; it never ships its own copy.
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          {ffc.found ? (
            <>
              <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-sm">
                <dt className="text-muted-foreground">Version</dt>
                <dd>{ffc.version || "Unknown"}</dd>
              </dl>
              <CopyField value={ffc.path} label="ffc location" copiedTitle="Path copied" />
              {ffc.error && (
                <Alert variant="destructive">
                  <IconAlertTriangle />
                  <AlertTitle>ffc did not answer</AlertTitle>
                  <AlertDescription>
                    Running it failed ({ffc.error}). Installing it again usually fixes this.
                  </AlertDescription>
                </Alert>
              )}
              {ffcUpdate && (
                <Alert>
                  <IconArrowUpCircle />
                  <AlertTitle>ffc {ffcUpdate.latest} is available</AlertTitle>
                  <AlertDescription>
                    Updating replaces the ffc above, where it is. Restart your connected assistants afterwards so they
                    use the new version.
                  </AlertDescription>
                </Alert>
              )}
              <p className="text-muted-foreground text-sm">
                The app looks for a newer ffc once a day (Check for updates in About looks now). You can also run{" "}
                <code className="bg-muted rounded px-1 py-0.5 font-mono text-xs">ffc update</code> in a terminal.
              </p>
            </>
          ) : (
            <div className="flex flex-col gap-2">
              <p className="text-muted-foreground text-sm">Install it here, or run this in a terminal yourself:</p>
              <CopyField value={env.data.installCommand} label="Install command" copiedTitle="Command copied" />
            </div>
          )}
        </CardContent>
        <CardFooter className="gap-2">
          {ok && ffcUpdate ? (
            <Button onClick={installFFC}>
              <IconArrowUpCircle data-icon="inline-start" />
              Update to {ffcUpdate.latest}
            </Button>
          ) : (
            <Button onClick={installFFC} variant={ok ? "outline" : "default"}>
              <IconDownload data-icon="inline-start" />
              {ffc.found ? "Install again" : "Install ffc"}
            </Button>
          )}
          <Button variant="outline" onClick={refresh} disabled={refreshing}>
            {refreshing ? <Spinner data-icon="inline-start" /> : <IconRefresh data-icon="inline-start" />}
            Look again
          </Button>
          {ffc.found && (
            <Button variant="ghost" onClick={() => attempt(backend.openFFCFolder)}>
              <IconFolderOpen data-icon="inline-start" />
              Open folder
            </Button>
          )}
        </CardFooter>
      </Card>
    </div>
  )
}

function UpdateResult({ result }: { result: UpdateOutcome }) {
  const { downloadUpdate, env } = useApp()
  const terminal = env.data?.os === "darwin" ? "Terminal" : "PowerShell"
  if (result.kind === "error") {
    return (
      <Alert variant="destructive">
        <IconAlertTriangle />
        <AlertTitle>{errorTitle(result.error)}</AlertTitle>
        <AlertDescription>{result.error.message}</AlertDescription>
      </Alert>
    )
  }
  if (result.kind === "available") {
    return (
      <Alert>
        <IconArrowUpCircle />
        <AlertTitle>Version {result.info.latest} is available</AlertTitle>
        <AlertDescription className="flex flex-col gap-2">
          {result.info.installCommand ? (
            <>
              <span>
                You have {result.info.current}.
                {result.info.publishedAt ? ` Released ${formatDate(result.info.publishedAt)}.` : ""} The easiest way
                to install it: run this in {terminal}. It checks the download, closes this app, installs the new
                version over it and opens it again, with no security warning to click through.
              </span>
              <CopyField value={result.info.installCommand} label="Install command" copiedTitle="Command copied" />
              <span>Or download it from GitHub and install it over this one.</span>
            </>
          ) : (
            <span>
              You have {result.info.current}.
              {result.info.publishedAt ? ` Released ${formatDate(result.info.publishedAt)}.` : ""} Download it from
              GitHub and install it over this one.
            </span>
          )}
        </AlertDescription>
        <AlertAction>
          <Button
            size="sm"
            variant={result.info.installCommand ? "outline" : "default"}
            onClick={() => void downloadUpdate(result.info)}
          >
            <IconDownload data-icon="inline-start" />
            Download
          </Button>
        </AlertAction>
      </Alert>
    )
  }
  return (
    <Alert>
      <IconCircleCheck />
      <AlertTitle>The app is up to date</AlertTitle>
      <AlertDescription>Version {result.info.current} is the newest one.</AlertDescription>
    </Alert>
  )
}

type UpdateOutcome =
  | { kind: "uptodate"; info: UpdateInfo }
  | { kind: "available"; info: UpdateInfo }
  | { kind: "error"; error: AppError }

function formatDate(iso: string) {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? "" : d.toLocaleDateString(undefined, { dateStyle: "medium" })
}

function AboutTab() {
  const { env, update, checkingUpdate, checkUpdate, ffcUpdate, installFFC } = useApp()
  // What the button found; before any click, what the startup check found.
  const [clicked, setClicked] = React.useState<UpdateOutcome | null>(null)
  const result: UpdateOutcome | null =
    clicked ?? (update?.available ? { kind: "available", info: update } : null)

  async function check() {
    try {
      const info = await checkUpdate()
      setClicked({ kind: info.available ? "available" : "uptodate", info })
    } catch (err) {
      setClicked({ kind: "error", error: appError(err) })
    }
  }

  const os = env.data?.os === "darwin" ? "macOS" : env.data?.os === "windows" ? "Windows" : env.data?.os
  return (
    <div className="flex max-w-2xl flex-col gap-6">
      <div className="flex items-center gap-4">
        <BrandLogo size={64} className="rounded-xl" />
        <div className="flex flex-col gap-1">
          <h2 className="font-heading text-lg font-semibold">Foxmayn Frappe Desktop</h2>
          <p className="text-muted-foreground text-sm">
            Version {env.data?.appVersion ?? "…"}
            {os ? ` · ${os}` : ""}
          </p>
        </div>
      </div>
      <p className="text-sm">
        Connects your Frappe and ERPNext sites to the AI assistants on your computer, using the ffc command line
        underneath. Made by Foxmayn. For Windows and macOS.
      </p>
      <Alert>
        <IconAlertTriangle />
        <AlertTitle>Not an official Frappe product.</AlertTitle>
        <AlertDescription>
          Foxmayn makes this app independently. It is not affiliated with or endorsed by Frappe Technologies.
        </AlertDescription>
      </Alert>
      <div className="flex flex-col gap-3">
        <div>
          <Button variant="outline" onClick={check} disabled={checkingUpdate}>
            {checkingUpdate ? <Spinner data-icon="inline-start" /> : <IconRefresh data-icon="inline-start" />}
            Check for updates
          </Button>
        </div>
        <div aria-live="polite" className="flex flex-col gap-3">
          {result && <UpdateResult result={result} />}
          {ffcUpdate && (
            <Alert>
              <IconArrowUpCircle />
              <AlertTitle>ffc {ffcUpdate.latest} is available</AlertTitle>
              <AlertDescription>You have ffc {ffcUpdate.current}.</AlertDescription>
              <AlertAction>
                <Button size="sm" onClick={installFFC}>
                  Update ffc
                </Button>
              </AlertAction>
            </Alert>
          )}
        </div>
      </div>
      <div className="flex flex-wrap gap-2">
        <Button variant="outline" onClick={() => attempt(() => backend.openWebsite(REPO))}>
          <IconBrandGithub data-icon="inline-start" />
          Source code
        </Button>
        <Button variant="outline" onClick={() => attempt(() => backend.openWebsite(`${REPO}/issues`))}>
          <IconBug data-icon="inline-start" />
          Report a problem
        </Button>
      </div>
    </div>
  )
}
