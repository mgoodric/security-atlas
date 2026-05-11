// Atlas environment config for security-atlas. Source of truth for the
// schema is the versioned SQL files under migrations/sql/. Atlas applies and
// rolls them back; the actual SQL is hand-authored.
//
// Local + CI invocation:
//   atlas migrate apply -c file://migrations/atlas.hcl --env local

variable "url" {
  type    = string
  default = getenv("DATABASE_URL")
}

env "local" {
  url = var.url
  migration {
    dir = "file://migrations/sql"
  }
}
