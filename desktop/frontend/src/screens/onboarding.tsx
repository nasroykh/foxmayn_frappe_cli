import { IconArrowRight, IconInfoCircle, IconRobot, IconWorld } from "@tabler/icons-react"
import * as React from "react"
import { useTranslation } from "react-i18next"

import { BrandLogo } from "@/components/brand-logo"
import { LanguageSwitcher } from "@/components/language-switcher"
import { FFCMissingAlert } from "@/components/page"
import { Button } from "@/components/ui/button"
import {
  Carousel,
  CarouselContent,
  CarouselItem,
  CarouselNext,
  CarouselPrevious,
  type CarouselApi,
} from "@/components/ui/carousel"
import { cn } from "cn"

/** The slides: the media here, the texts under welcome.slides.<id> in the catalog. */
const slides = [
  { id: "welcome", media: <BrandLogo size={96} className="rounded-2xl" /> },
  { id: "sites", media: <IconWorld /> },
  { id: "apps", media: <IconRobot /> },
]

// i18n keys: welcome.slides.welcome.title welcome.slides.welcome.body welcome.slides.sites.title welcome.slides.sites.body welcome.slides.apps.title welcome.slides.apps.body
/** First run: what the app does, in three slides. */
export function Onboarding({ onDone }: { onDone: (addSite: boolean) => void }) {
  const { t } = useTranslation()
  const [api, setAPI] = React.useState<CarouselApi>()
  const [index, setIndex] = React.useState(0)

  React.useEffect(() => {
    if (!api) return
    const update = () => setIndex(api.selectedScrollSnap())
    update()
    api.on("select", update)
    return () => {
      api.off("select", update)
    }
  }, [api])

  const last = index === slides.length - 1

  return (
    <main className="bg-background flex min-h-svh flex-col items-center justify-center gap-8 p-8">
      <Carousel setApi={setAPI} className="w-full max-w-md" aria-label={t("welcome.label")}>
        <CarouselContent>
          {slides.map((s) => (
            <CarouselItem key={s.id}>
              <div className="flex flex-col items-center gap-5 px-2 text-center">
                <div className="bg-muted text-foreground flex size-24 items-center justify-center rounded-2xl [&_svg:not([class*='size-'])]:size-12">
                  {s.media}
                </div>
                <h1 className="font-heading text-2xl font-semibold tracking-tight text-balance">{t(`welcome.slides.${s.id}.title`)}</h1>
                <p className="text-muted-foreground text-balance">{t(`welcome.slides.${s.id}.body`)}</p>
              </div>
            </CarouselItem>
          ))}
        </CarouselContent>
        <CarouselPrevious />
        <CarouselNext />
      </Carousel>

      <div className="flex items-center gap-2" role="tablist" aria-label={t("welcome.slidesLabel")}>
        {slides.map((s, i) => (
          <button
            key={s.id}
            type="button"
            role="tab"
            aria-selected={i === index}
            aria-label={t("welcome.slideLabel", { n: i + 1, title: t(`welcome.slides.${s.id}.title`) })}
            onClick={() => api?.scrollTo(i)}
            className={cn(
              "focus-visible:ring-ring/50 h-2 rounded-full transition-all outline-none focus-visible:ring-3",
              i === index ? "bg-primary w-6" : "bg-muted-foreground/30 w-2",
            )}
          />
        ))}
      </div>

      <div className="flex w-full max-w-md flex-col gap-4">
        {index === 0 && <LanguageSwitcher className="self-center" />}
        {last && <FFCMissingAlert />}
        <div className="flex items-center justify-center gap-2">
          <Button variant="ghost" onClick={() => onDone(false)}>
            {last ? t("welcome.lookAround") : t("common.skip")}
          </Button>
          {last ? (
            <Button onClick={() => onDone(true)}>
              {t("welcome.addFirstSite")}
              <IconArrowRight data-icon="inline-end" />
            </Button>
          ) : (
            <Button onClick={() => api?.scrollNext()}>
              {t("common.next")}
              <IconArrowRight data-icon="inline-end" />
            </Button>
          )}
        </div>
      </div>
      <p className="text-muted-foreground flex items-center gap-1.5 text-xs">
        <IconInfoCircle className="size-3.5" />
        {t("welcome.notOfficial")}
      </p>
    </main>
  )
}
