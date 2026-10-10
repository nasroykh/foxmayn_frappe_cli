import { IconArrowRight, IconInfoCircle, IconRobot, IconWorld } from "@tabler/icons-react"
import * as React from "react"

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

const slides = [
  {
    media: <BrandLogo size={96} className="rounded-2xl" />,
    title: "Welcome to Foxmayn Frappe Desktop",
    body: "Connect your Frappe and ERPNext sites to the AI assistants on this computer, like Claude, Cursor and VS Code. No terminal needed.",
  },
  {
    media: <IconWorld />,
    title: "Add your sites",
    body: "Sign in once with your browser. Your sign-in stays on this computer, and you can check or remove it at any time.",
  },
  {
    media: <IconRobot />,
    title: "Connect your assistants",
    body: "Pick an assistant and a site, and see exactly what changes before anything is saved. Turn on read-only to let it look without changing anything.",
  },
]

/** First run: what the app does, in three slides. */
export function Onboarding({ onDone }: { onDone: (addSite: boolean) => void }) {
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
      <Carousel setApi={setAPI} className="w-full max-w-md" aria-label="Welcome">
        <CarouselContent>
          {slides.map((s) => (
            <CarouselItem key={s.title}>
              <div className="flex flex-col items-center gap-5 px-2 text-center">
                <div className="bg-muted text-foreground flex size-24 items-center justify-center rounded-2xl [&_svg:not([class*='size-'])]:size-12">
                  {s.media}
                </div>
                <h1 className="font-heading text-2xl font-semibold tracking-tight text-balance">{s.title}</h1>
                <p className="text-muted-foreground text-balance">{s.body}</p>
              </div>
            </CarouselItem>
          ))}
        </CarouselContent>
        <CarouselPrevious />
        <CarouselNext />
      </Carousel>

      <div className="flex items-center gap-2" role="tablist" aria-label="Slides">
        {slides.map((s, i) => (
          <button
            key={s.title}
            type="button"
            role="tab"
            aria-selected={i === index}
            aria-label={`Slide ${i + 1}: ${s.title}`}
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
            {last ? "Look around first" : "Skip"}
          </Button>
          {last ? (
            <Button onClick={() => onDone(true)}>
              Add your first site
              <IconArrowRight data-icon="inline-end" />
            </Button>
          ) : (
            <Button onClick={() => api?.scrollNext()}>
              Next
              <IconArrowRight data-icon="inline-end" />
            </Button>
          )}
        </div>
      </div>
      <p className="text-muted-foreground flex items-center gap-1.5 text-xs">
        <IconInfoCircle className="size-3.5" />
        Not an official Frappe product.
      </p>
    </main>
  )
}
