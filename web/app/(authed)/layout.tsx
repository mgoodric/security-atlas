import { cookies } from "next/headers"
import { redirect } from "next/navigation"

import { Sidebar } from "@/components/shell/sidebar"
import { TopBar } from "@/components/shell/topbar"
import { SESSION_COOKIE } from "@/lib/auth"

export default async function AuthedLayout({
  children,
}: {
  children: React.ReactNode
}) {
  const jar = await cookies()
  if (!jar.get(SESSION_COOKIE)?.value) {
    redirect("/login")
  }

  return (
    <div className="flex h-screen flex-col">
      <TopBar />
      <div className="flex flex-1 overflow-hidden">
        <Sidebar />
        <main className="flex-1 overflow-y-auto p-6">{children}</main>
      </div>
    </div>
  )
}
