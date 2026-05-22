# sdk/

Non-Go SDKs for the security-atlas evidence push API.

| SDK               | Workspace                                   | First slice |
| ----------------- | ------------------------------------------- | ----------- |
| `sdk/python/`     | `uv` workspace member                       | 003         |
| `sdk/typescript/` | npm workspace `@security-atlas/sdk`         | 003         |
| `sdk/java/`       | Maven module `com.security-atlas:atlas-sdk` | 195         |

Slice 001 ships empty package skeletons for `sdk/python/` and `sdk/typescript/` so workspace tooling (`uv`, `npm install`) recognizes them. Slice 195 adds the Java SDK as a Maven module with the OAuth `client_credentials` helper.
