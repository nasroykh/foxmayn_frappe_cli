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
import { Trans, useTranslation } from "react-i18next"

import { useApp } from "@/app/app-context"
import { useTheme, type Theme } from "@/app/theme"
import { BrandLogo } from "@/components/brand-logo"
import { CopyField } from "@/components/copy-field"
import { LanguageSwitcher } from "@/components/language-switcher"
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
import { intlLocale } from "@/i18n"
import { appError, errorTitle, type AppError } from "@/lib/errors"
import { ProfileSettings } from "@/screens/assistant/profile-settings"
import { HistorySettings } from "@/screens/assistant/history-settings"
import { ProviderSettings } from "@/screens/assistant/provider-settings"
import { SiteAssistantSettings } from "@/screens/assistant/site-settings"
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
  const { t } = useTranslation()
  return (
    <div className="flex flex-col gap-6">
      <PageHeader title={t("shell.nav.settings")} description={t("settings.description")} />
      <Tabs defaultValue="general">
        <TabsList>
          <TabsTrigger value="general">{t("settings.generalTab")}</TabsTrigger>
          <TabsTrigger value="assistant">{t("settings.assistantTab")}</TabsTrigger>
          <TabsTrigger value="ffc">{t("settings.ffcTab")}</TabsTrigger>
          <TabsTrigger value="about">{t("settings.aboutTab")}</TabsTrigger>
        </TabsList>
        <TabsContent value="general" className="pt-4">
          <GeneralTab />
        </TabsContent>
        <TabsContent value="assistant" className="flex flex-col gap-8 pt-4">
          <ProviderSettings />
          <Separator />
          <ProfileSettings />
          <Separator />
          <SiteAssistantSettings />
          <Separator />
          <HistorySettings />
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
  const { t } = useTranslation()
  const { theme, setTheme } = useTheme()
  const { env } = useApp()
  return (
    <div className="flex max-w-2xl flex-col gap-6">
      <FieldGroup>
        <Field orientation="horizontal">
          <FieldContent>
            <FieldTitle>{t("language.label")}</FieldTitle>
            <FieldDescription>{t("language.description")}</FieldDescription>
          </FieldContent>
          <LanguageSwitcher />
        </Field>
        <Separator />
        <Field orientation="horizontal">
          <FieldContent>
            <FieldTitle>{t("theme.title")}</FieldTitle>
            <FieldDescription>{t("theme.description")}</FieldDescription>
          </FieldContent>
          <ToggleGroup
            variant="outline"
            value={[theme]}
            onValueChange={(v: string[]) => v[0] && setTheme(v[0] as Theme)}
            aria-label={t("theme.title")}
          >
            <ToggleGroupItem value="light" aria-label={t("theme.light")}>
              <IconSun data-icon="inline-start" />
              {t("theme.light")}
            </ToggleGroupItem>
            <ToggleGroupItem value="dark" aria-label={t("theme.dark")}>
              <IconMoon data-icon="inline-start" />
              {t("theme.dark")}
            </ToggleGroupItem>
            <ToggleGroupItem value="system" aria-label={t("theme.system")}>
              <IconDeviceDesktop data-icon="inline-start" />
              {t("theme.systemShort")}
            </ToggleGroupItem>
          </ToggleGroup>
        </Field>
        <Separator />
        <Field>
          <FieldLabel htmlFor="config-path">{t("settings.configFile")}</FieldLabel>
          {env.data ? (
            <div className="flex gap-2">
              <div className="min-w-0 flex-1">
                <CopyField value={env.data.configPath} label={t("settings.configPath")} copiedTitle={t("settings.pathCopied")} />
              </div>
              <Button
                variant="outline"
                onClick={() => attempt(backend.openConfigFolder)}
                disabled={!env.data.configExists}
              >
                <IconFolderOpen data-icon="inline-start" />
                {t("settings.openFolder")}
              </Button>
            </div>
          ) : (
            <Skeleton className="h-8 w-full" />
          )}
          <FieldDescription>
            {env.data && !env.data.configExists
              ? t("settings.configMissing")
              : t("settings.configShared")}
          </FieldDescription>
        </Field>
      </FieldGroup>
      <WSLAlert />
    </div>
  )
}

function FFCTab() {
  const { t } = useTranslation()
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
            {t("settings.ffcTab")}
            {ok && ffcUpdate ? (
              <Badge variant="secondary">
                <IconArrowUpCircle data-icon="inline-start" />
                {t("settings.ffc.updateAvailable")}
              </Badge>
            ) : ok ? (
              <Badge>
                <IconCircleCheck data-icon="inline-start" />
                {t("settings.ffc.installed")}
              </Badge>
            ) : (
              <Badge variant="destructive">
                <IconAlertTriangle data-icon="inline-start" />
                {ffc.found ? t("settings.ffc.notWorking") : t("settings.ffc.notInstalled")}
              </Badge>
            )}
          </CardTitle>
          <CardDescription>
            {t("settings.ffc.description")}
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          {ffc.found ? (
            <>
              <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-sm">
                <dt className="text-muted-foreground">{t("settings.ffc.version")}</dt>
                <dd>{ffc.version || t("settings.ffc.unknown")}</dd>
              </dl>
              <CopyField value={ffc.path} label={t("settings.ffc.location")} copiedTitle={t("settings.pathCopied")} />
              {ffc.error && (
                <Alert variant="destructive">
                  <IconAlertTriangle />
                  <AlertTitle>{t("settings.ffc.noAnswerTitle")}</AlertTitle>
                  <AlertDescription>
                    {ffc.manager
                      ? t("settings.ffc.noAnswerManager", { error: ffc.error, manager: ffc.manager })
                      : t("settings.ffc.noAnswer", { error: ffc.error })}
                  </AlertDescription>
                </Alert>
              )}
              {ffcUpdate && (
                <Alert>
                  <IconArrowUpCircle />
                  <AlertTitle>{t("shell.update.ffcTitle", { version: ffcUpdate.latest })}</AlertTitle>
                  <AlertDescription>{t("settings.ffc.updateBody")}</AlertDescription>
                </Alert>
              )}
              <p className="text-muted-foreground text-sm">
                <Trans
                  i18nKey="settings.ffc.autoCheck"
                  values={{ command: ffc.upgradeCommand || "ffc update" }}
                  components={{ code: <code className="bg-muted rounded px-1 py-0.5 font-mono text-xs" /> }}
                />
              </p>
            </>
          ) : (
            <div className="flex flex-col gap-2">
              <p className="text-muted-foreground text-sm">{t("settings.ffc.installHere")}</p>
              <CopyField
                value={env.data.installCommand}
                label={t("settings.installCommand")}
                copiedTitle={t("shell.update.commandCopied")}
              />
            </div>
          )}
        </CardContent>
        <CardFooter className="gap-2">
          {ok && ffcUpdate ? (
            <Button onClick={installFFC}>
              <IconArrowUpCircle data-icon="inline-start" />
              {t("settings.ffc.updateTo", { version: ffcUpdate.latest })}
            </Button>
          ) : (
            !ffc.manager && (
              <Button onClick={installFFC} variant={ok ? "outline" : "default"}>
                <IconDownload data-icon="inline-start" />
                {ffc.found ? t("settings.ffc.installAgain") : t("settings.ffc.install")}
              </Button>
            )
          )}
          <Button variant="outline" onClick={refresh} disabled={refreshing}>
            {refreshing ? <Spinner data-icon="inline-start" /> : <IconRefresh data-icon="inline-start" />}
            {t("settings.ffc.lookAgain")}
          </Button>
          {ffc.found && (
            <Button variant="ghost" onClick={() => attempt(backend.openFFCFolder)}>
              <IconFolderOpen data-icon="inline-start" />
              {t("settings.openFolder")}
            </Button>
          )}
        </CardFooter>
      </Card>
    </div>
  )
}

function UpdateResult({ result }: { result: UpdateOutcome }) {
  const { t } = useTranslation()
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
    const released = result.info.publishedAt ? formatDate(result.info.publishedAt) : ""
    const youHave = [
      t("settings.update.youHave", { current: result.info.current }),
      released ? t("settings.update.released", { date: released }) : "",
    ]
      .filter(Boolean)
      .join(" ")
    return (
      <Alert>
        <IconArrowUpCircle />
        <AlertTitle>{t("settings.update.availableTitle", { version: result.info.latest })}</AlertTitle>
        <AlertDescription className="flex flex-col gap-2">
          {result.info.installCommand ? (
            <>
              <span>
                {youHave} {t("settings.update.easiestWay", { terminal })}
              </span>
              <CopyField
                value={result.info.installCommand}
                label={t("settings.installCommand")}
                copiedTitle={t("shell.update.commandCopied")}
              />
              <span>{t("settings.update.orDownload")}</span>
            </>
          ) : (
            <span>
              {youHave} {t("settings.update.downloadFromGitHub")}
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
            {t("common.download")}
          </Button>
        </AlertAction>
      </Alert>
    )
  }
  return (
    <Alert>
      <IconCircleCheck />
      <AlertTitle>{t("settings.update.upToDateTitle")}</AlertTitle>
      <AlertDescription>{t("settings.update.upToDateBody", { version: result.info.current })}</AlertDescription>
    </Alert>
  )
}

type UpdateOutcome =
  | { kind: "uptodate"; info: UpdateInfo }
  | { kind: "available"; info: UpdateInfo }
  | { kind: "error"; error: AppError }

function formatDate(iso: string) {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? "" : d.toLocaleDateString(intlLocale(), { dateStyle: "medium" })
}

function AboutTab() {
  const { t } = useTranslation()
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
            {t("settings.about.version", { version: env.data?.appVersion ?? "…" })}
            {os ? ` · ${os}` : ""}
          </p>
        </div>
      </div>
      <p className="text-sm">
        {t("settings.about.summary")}
      </p>
      <Alert>
        <IconAlertTriangle />
        <AlertTitle>{t("welcome.notOfficial")}</AlertTitle>
        <AlertDescription>{t("settings.about.independent")}</AlertDescription>
      </Alert>
      <div className="flex flex-col gap-3">
        <div>
          <Button variant="outline" onClick={check} disabled={checkingUpdate}>
            {checkingUpdate ? <Spinner data-icon="inline-start" /> : <IconRefresh data-icon="inline-start" />}
            {t("settings.about.checkUpdates")}
          </Button>
        </div>
        <div aria-live="polite" className="flex flex-col gap-3">
          {result && <UpdateResult result={result} />}
          {ffcUpdate && (
            <Alert>
              <IconArrowUpCircle />
              <AlertTitle>{t("shell.update.ffcTitle", { version: ffcUpdate.latest })}</AlertTitle>
              <AlertDescription>{t("settings.about.ffcYouHave", { current: ffcUpdate.current })}</AlertDescription>
              <AlertAction>
                <Button size="sm" onClick={installFFC}>
                  {t("settings.about.updateFfc")}
                </Button>
              </AlertAction>
            </Alert>
          )}
        </div>
      </div>
      <div className="flex flex-wrap gap-2">
        <Button variant="outline" onClick={() => attempt(() => backend.openWebsite(REPO))}>
          <IconBrandGithub data-icon="inline-start" />
          {t("settings.about.sourceCode")}
        </Button>
        <Button variant="outline" onClick={() => attempt(() => backend.openWebsite(`${REPO}/issues`))}>
          <IconBug data-icon="inline-start" />
          {t("settings.about.reportProblem")}
        </Button>
      </div>
    </div>
  )
}
