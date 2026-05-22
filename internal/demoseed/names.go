// Package demoseed produces a comprehensive demo dataset for a single
// tenant (slice 205).
//
// Names + fictional-company tokens are sourced from this curated list
// (decision D2). Hand-curated rather than Faker-generated to:
//
//   - Avoid the "always the same 100 Faker en_US names" pattern-match
//     risk (a security buyer doing diligence might recognize Faker's
//     defaults).
//   - Keep the set small enough to manually sanity-scan for accidental
//     real-PII overlap (the maintainer ran the scan before merge; see
//     decisions log).
//   - Keep the asset / vendor names obviously fictional ("Pinecone Bank"
//     not "Acme Corp") so screenshots do not require redaction before
//     publication.
//
// AC-2 + threat-model row "I Info disclosure" both gate on this list
// being deterministic + sanity-scanned. Any addition MUST follow the
// same review.
//
// The seeder picks names with a deterministic per-fixture index, so the
// same seed run produces the same name-to-row mapping (idempotency,
// AC-4). A different --tenant-slug seeds a new tenant but uses the same
// name pool — the slug ties the dataset to a tenant, not to a name
// permutation.
package demoseed

// fictionalPeople is the hand-curated pool of ~50 first/last name pairs
// for synthetic demo users. The mix spans common Western + East-Asian +
// South-Asian + Hispanic names so a security buyer screenshot does not
// look mono-cultural. NONE of these names are real employees of
// security-atlas, real authors of cited blog posts, or otherwise
// real-PII-shaped. Manual sanity scan: every entry passed (2026-05-22).
var fictionalPeople = []struct {
	First string
	Last  string
}{
	{"Avery", "Castellan"},
	{"Bashir", "Demirci"},
	{"Cassandra", "Okonkwo"},
	{"Devon", "Pemberton"},
	{"Elara", "Marwick"},
	{"Felix", "Yoshida"},
	{"Greta", "Sandoval"},
	{"Harper", "Vinokurov"},
	{"Ines", "Beauregard"},
	{"Jasper", "Khatun"},
	{"Kiona", "Larsen"},
	{"Lior", "Maestro"},
	{"Mira", "Northcutt"},
	{"Nile", "Osterberg"},
	{"Octavia", "Pendleton"},
	{"Pascal", "Quartermaine"},
	{"Quinn", "Ravenscroft"},
	{"Rohan", "Stavros"},
	{"Saoirse", "Trelawney"},
	{"Tarun", "Ungaretti"},
	{"Una", "Vaszary"},
	{"Vidya", "Wickham"},
	{"Wren", "Xenakis"},
	{"Xiomara", "Yardley"},
	{"Yusuf", "Zaragoza"},
	{"Zara", "Aldoubas"},
	{"Bram", "Boudreau"},
	{"Cleo", "Constantine"},
	{"Dominic", "Drysdale"},
	{"Eulalia", "Estevez"},
	{"Finn", "Fitzwilliam"},
	{"Galia", "Gallenkamp"},
	{"Hideo", "Halifax"},
	{"Idris", "Inkpen"},
	{"Jovita", "Janowicz"},
	{"Kasper", "Kellerman"},
	{"Lakshmi", "Liddell"},
	{"Mateo", "Moncrieff"},
	{"Nadira", "Nakamura"},
	{"Oren", "Oluwasanmi"},
	{"Priya", "Pendragon"},
	{"Quentin", "Quintanilla"},
	{"Rosalind", "Ruthersfield"},
	{"Simeon", "Sinclair"},
	{"Tomasz", "Tarkanian"},
	{"Ursula", "Underhill"},
	{"Vivienne", "Vandermeer"},
	{"Walden", "Worthington"},
	{"Xavier", "Yamashiro"},
	{"Yelena", "Zinkovsky"},
}

// fictionalVendors is the curated pool of fake third-party vendor
// names. NONE of these are real companies as of 2026-05-22 (manual scan
// performed). They follow common SaaS/tooling naming patterns
// (food-noun-Y, geological-feature-suffix, etc.) so they look
// plausibly real without colliding with real products.
var fictionalVendors = []struct {
	Name   string
	Domain string
}{
	{"Pinecone Bank", "pineconebank.example"},
	{"Riverstone Analytics", "riverstone-analytics.example"},
	{"Marlowe Logistics", "marlowelogistics.example"},
	{"Granite Sky CDN", "granitesky.example"},
	{"Halcyon Telephony", "halcyontel.example"},
	{"Vellum Print Services", "vellumprint.example"},
	{"Cloudberry Mail", "cloudberrymail.example"},
	{"Driftwood Logging", "driftwoodlogging.example"},
	{"Saffron Identity Co", "saffronid.example"},
	{"Tessellate Maps API", "tessellatemaps.example"},
	{"Quill & Co Bookkeeping", "quillco.example"},
	{"Mariposa Payroll", "mariposapayroll.example"},
	{"Beacon Hill Insurance", "beaconhillins.example"},
	{"Lapis Recruiting", "lapisrecruit.example"},
	{"Thornfield Background Checks", "thornfieldbg.example"},
}

// fictionalAssets is the curated pool of fake internal asset / system
// names. Used to populate evidence captions ("scan of {asset}",
// "access review of {asset}"). Designed to read like a real
// company's infrastructure inventory without colliding with any
// real-product names.
var fictionalAssets = []string{
	"customer-portal-prod",
	"customer-portal-staging",
	"billing-svc-prod",
	"billing-svc-staging",
	"ml-feature-store",
	"data-warehouse-bi",
	"hr-onboarding-app",
	"vendor-portal-eu",
	"vendor-portal-us",
	"internal-wiki",
	"build-runner-fleet",
	"artifact-mirror",
	"observability-stack",
	"sso-prod",
	"sso-staging",
	"sandbox-eks-cluster",
	"red-team-lab",
	"backup-vault-cold",
	"backup-vault-hot",
	"key-management-svc",
}

// fictionalCompanyName is the demo tenant's display-name suffix. The
// CLI computes "<slug> Demo" by default; callers may override via
// --display-name. This constant exists so docs + tests reference a
// stable string.
const fictionalCompanyName = "Demo"

// personEmailDomain is the email domain stamped on every synthesized
// user. RFC 2606 reserves `.example` for documentation use exactly so
// nobody owns this domain and the demo creds are obviously fictional
// in any screenshot.
const personEmailDomain = "demo.example"
