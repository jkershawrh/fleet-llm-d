import type { Metadata } from 'next'
import { Sidebar } from '@/components/sidebar'
import './globals.css'

export const metadata: Metadata = {
  title: 'fleet-llm-d Dashboard',
  description: 'Fleet inference management dashboard for llm-d',
}

export default function RootLayout({
  children,
}: {
  children: React.ReactNode
}) {
  return (
    <html lang="en" className="dark">
      <body className="min-h-screen bg-background antialiased">
        <Sidebar />
        <div className="pl-60">
          {/* Header Bar */}
          <header className="sticky top-0 z-30 flex h-16 items-center justify-between border-b border-border bg-background/80 px-8 backdrop-blur-sm">
            <div />
            <div className="flex items-center gap-4">
              <div className="flex items-center gap-2 rounded-lg border border-border px-3 py-1.5">
                <div className="h-2 w-2 rounded-full bg-emerald-500 animate-pulse" />
                <span className="text-xs font-medium text-muted-foreground">Fleet Healthy</span>
              </div>
              <div className="text-xs text-muted-foreground">
                {/* Render timestamp client-side to avoid hydration mismatch */}
                <FleetTimestamp />
              </div>
            </div>
          </header>

          {/* Page Content */}
          <main className="p-8">{children}</main>
        </div>
      </body>
    </html>
  )
}

function FleetTimestamp() {
  // Server-render a static placeholder; the client will hydrate with the live clock
  return <span suppressHydrationWarning>UTC --:--</span>
}
