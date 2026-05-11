import { signOut } from "@/app/login/actions";
import { Button } from "@/components/ui/button";

export function TopBar() {
  return (
    <header className="flex h-14 shrink-0 items-center justify-between border-b bg-background px-6">
      <div className="flex items-center gap-3">
        <span className="text-base font-semibold">security-atlas</span>
        <span className="text-xs text-muted-foreground">v0 · self-host</span>
      </div>
      <form action={signOut}>
        <Button type="submit" variant="ghost" size="sm">
          Sign out
        </Button>
      </form>
    </header>
  );
}
