- **tenants:** new nullable `slug TEXT` column + partial UNIQUE
  index on non-NULL slugs; new nullable `created_by_user_id UUID`
  column (slice 143).
