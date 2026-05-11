import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Button } from "@/components/ui/button"
import { Alert, AlertDescription } from "@/components/ui/alert"

import { signIn } from "./actions"

type SearchParams = Promise<{ from?: string; error?: string }>

export default async function LoginPage({
  searchParams,
}: {
  searchParams: SearchParams
}) {
  const { from, error } = await searchParams

  return (
    <div className="flex min-h-screen items-center justify-center bg-muted/30 p-4">
      <Card className="w-full max-w-md">
        <CardHeader>
          <CardTitle>Sign in to security-atlas</CardTitle>
          <CardDescription>
            Paste a bearer token issued by <code>atlas-cli credentials issue</code>{" "}
            or printed to stderr at server startup.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {error ? (
            <Alert variant="destructive" className="mb-4">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          ) : null}
          <form action={signIn} className="space-y-4">
            <input type="hidden" name="from" value={from ?? "/dashboard"} />
            <div className="space-y-2">
              <label htmlFor="token" className="text-sm font-medium">
                Bearer token
              </label>
              <Input
                id="token"
                name="token"
                type="password"
                placeholder="long opaque string"
                required
                autoComplete="off"
              />
            </div>
            <Button type="submit" className="w-full">
              Sign in
            </Button>
          </form>
        </CardContent>
      </Card>
    </div>
  )
}
