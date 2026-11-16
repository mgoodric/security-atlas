- **users.demo_only:** new `BOOLEAN NOT NULL DEFAULT FALSE` column
  (slice 205) tagging users created by the `atlas-cli demo seed`
  command. Default FALSE for every existing row + every non-demo
  user created in the future. A future slice will gate the slice-142
  super_admin promotion path against `demo_only=TRUE` users
  (preventing demo users from being promoted to super_admin as a
  backdoor; addresses the slice 205 threat-model row "E EoP").
