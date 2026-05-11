import Link from "next/link";

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";

export default function DashboardPage() {
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Dashboard</h1>
        <p className="text-sm text-muted-foreground">
          Tracer-bullet shell. Real data lands in the Catalog · SCF page;
          program-level dashboards land in slice 040.
        </p>
      </div>
      <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
        <Card>
          <CardHeader>
            <CardTitle>SCF anchor browser</CardTitle>
            <CardDescription>
              Inspect SCF control anchors and the framework requirements that
              map to each.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <Link href="/catalog/scf" className="text-sm font-medium underline">
              Open catalog
            </Link>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Controls</CardTitle>
            <CardDescription>Lands with slice 009 + 010.</CardDescription>
          </CardHeader>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Evidence ledger</CardTitle>
            <CardDescription>Lands with slice 013 + 016.</CardDescription>
          </CardHeader>
        </Card>
      </div>
    </div>
  );
}
