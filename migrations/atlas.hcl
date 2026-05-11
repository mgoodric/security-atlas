// Atlas environment config for security-atlas.
//
// Source of truth for the schema is migrations/sql/ — versioned SQL files
// (forward + .down.sql reverse pairs). Atlas HCL `policy` / `row_security`
// blocks are Pro-only, so SQL is the declarative substrate.
//
// Local usage:
//   just migrate up         # atlas migrate apply --env local
//   just migrate down N     # atlas migrate down --env local --amount N
//   just migrate status     # atlas migrate status --env local
//
// CI uses DATABASE_URL injected by the GitHub Actions Postgres service.

variable "url" {
  type    = string
  default = getenv("DATABASE_URL")
}

variable "dev_url" {
  type    = string
  default = getenv("ATLAS_DEV_URL")
}

env "local" {
  url     = var.url
  dev     = var.dev_url
  migration {
    dir = "file://migrations/sql"
  }
  format {
    migrate {
      apply = "{{ json . }}"
    }
  }
}
