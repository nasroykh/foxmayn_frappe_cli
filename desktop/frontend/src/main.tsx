import React from "react"
import { CSPProvider } from "@base-ui/react/csp-provider"
import ReactDOM from "react-dom/client"

import App from "./App"
import { ThemeProvider } from "@/app/theme"
import { Toaster } from "@/components/ui/toast"
import { TooltipProvider } from "@/components/ui/tooltip"
import "./i18n"
import "./index.css"

ReactDOM.createRoot(document.getElementById("root") as HTMLElement).render(
  <React.StrictMode>
    <CSPProvider disableStyleElements>
    <ThemeProvider>
      <TooltipProvider>
        <Toaster>
          <App />
        </Toaster>
      </TooltipProvider>
    </ThemeProvider>
    </CSPProvider>
  </React.StrictMode>,
)
