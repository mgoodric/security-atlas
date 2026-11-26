# connectors/

Per-connector implementations. Each connector is its own subdirectory and may be written in any language; communication with the platform is over the gRPC connector contract (defined in `proto/` from slice 003).

v1 connector roster (per `docs/issues/_INDEX.md`):

| Connector                               | Slice | Notes                                                                 |
| --------------------------------------- | ----- | --------------------------------------------------------------------- |
| `connectors/aws/`                       | 004   | First-connector tracer bullet (S3 bucket encryption evidence_kind)    |
| `connectors/github/`                    | 044   | Repo settings, branch protection, audit log                           |
| `connectors/okta/`                      | 045   | IdP policy, MFA, SCIM                                                 |
| `connectors/1password/`                 | 046   | Org password policy                                                   |
| `connectors/osquery/`                   | 047   | Endpoint posture via Fleet (open-source, no proprietary agent)        |
| `connectors/jira/` `connectors/linear/` | 048   | Ticket evidence (change management, incident response)                |
| `connectors/manual/`                    | 049   | Universal escape hatch — CSV / S3 watcher / SFTP / UI upload          |
| `connectors/azure/`                     | 486   | Entra ID role assignments + Azure Storage account config              |
| `connectors/confluence/`                | 2856  | Policy sync from a Confluence Cloud space (`POST /v1/policies:sync`)  |
| `connectors/sharepoint/`                | 2837  | Policy sync from a SharePoint Online document library                 |
| `connectors/shared/policysync/`         | 2837  | Policy-sync client shared by the Confluence and SharePoint connectors |

Empty in slice 001.
