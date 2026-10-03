# Repository context contract

Repository context is a bounded, immutable source snapshot for the existing single-operator pilot. It informs proposals; it is not proof of deployed behaviour or an independent business oracle. Human expectations still take precedence over implementation-derived values.

## Import and review

`POST /api/projects/{id}/repositories/sync` accepts `{provider?: "github"|"azure", repository: string, ref: "main", role: "frontend"|"backend", paths: string[]}`. `paths` contains 1–20 exact repository-relative text file paths; the operator explicitly chooses what may be imported. Omitted `provider` defaults to GitHub; GitHub uses `owner/name` and optional `X-QA-GitHub-Token`, sent only to the fixed GitHub API origin. Azure uses a full `https://dev.azure.com/organization/project/_git/repository` URL and optional `X-QA-Azure-PAT`, sent as HTTP Basic authentication only to `https://dev.azure.com`. Credentials for the wrong provider (including both headers) are rejected. No token is stored, logged, or forwarded to the model. GitHub public repositories work without a token. Azure may require a Code (Read) PAT even for a public repository because its Git object APIs can deny anonymous access; the form reports this clearly. Private repositories require a read-capable token for each sync. Azure PATs need **Code (Read)** access for the selected repository. Tokens are cleared from the form on submit, cancel, provider change, project change, or disconnect.

The importer resolves the ref to a commit SHA, reads all requested files at that commit, and returns errors rather than silently dropping or truncating files. Only regular text blobs are accepted, no symlinks/submodules. Reject environment/key/credential files, binary content, recognizable embedded secrets, unsafe paths, and oversized content. These checks are an aid, not a guarantee that source contains no sensitive material; review the complete imported content before consenting to send it to a provider. Limit to 40,000 bytes of file content per snapshot and 60,000 bytes of assembled proposal context. Import never executes repository code.

`GET /api/projects/{id}/repositories` returns at most 100 newest snapshot summaries as an array. Summary fields: `id`, `project_id`, `provider`, `repository`, `ref`, `role`, `commit_sha`, `content_sha256`, `file_count`, `total_bytes`, `created_at`.

`GET /api/projects/{id}/repositories/{snapshot_id}` returns the summary plus `files: [{path, content}]`. Creating a new snapshot does not mutate earlier records. Re-sync uses the same form and produces a new snapshot pinned to the newly resolved commit. The initial UI supports deliberate sync; automatic webhook sync comes after this contract.

## Azure Repos details

Azure DevOps **Services** Git repositories are supported. Self-hosted Azure DevOps Server, TFVC, SSH clone URLs, and legacy `visualstudio.com` URLs are not supported; use the canonical `dev.azure.com` URL. Project and repository names containing spaces are URL-escaped. Arbitrary hosts, URL credentials, queries, fragments, and path traversal are rejected.

A bare ref means an Azure branch (for example `main` or `feature/login`). Use `refs/tags/v1` for tags, `refs/heads/main` for explicit branches, or a full 40-character commit SHA. Ref prefix matches are never accepted as exact matches. Annotated tags are peeled to commits. Ref lookup is bounded to 1,000 prefix matches; if the exact ref is absent, provide its full commit SHA. The commit's tree is resolved once; selected regular files are fetched by immutable blob IDs. Symlinks and submodules are rejected. Git LFS is not resolved.

Authentication currently uses an import-only PAT, matching this local pilot's transient GitHub token flow. Microsoft recommends [Microsoft Entra authentication for production integrations](https://learn.microsoft.com/en-us/azure/devops/organizations/accounts/use-personal-access-tokens-to-authenticate?view=azure-devops); account sign-in/OAuth and automatic sync remain future work. Protocols follow Microsoft's [refs](https://learn.microsoft.com/en-us/rest/api/azure/devops/git/refs/list?view=azure-devops-rest-7.1), [commits](https://learn.microsoft.com/en-us/rest/api/azure/devops/git/commits/get?view=azure-devops-rest-7.1), [trees](https://learn.microsoft.com/en-us/rest/api/azure/devops/git/trees/get?view=azure-devops-rest-7.1), and [blobs](https://learn.microsoft.com/en-us/rest/api/azure/devops/git/blobs/get-blob?view=azure-devops-rest-7.1) APIs (7.1).

## Proposals

Existing proposal input gains optional `repository_snapshot_ids: string[]` (at most two, unique, same project, at most one per role). `context` may be empty only when snapshots are selected. The server builds one bounded context from operator context and snapshot headers/files; validation and consent apply to that final text. The response adds `repository_snapshots`, containing the selected summaries. The existing `context_sha256` hashes the complete context actually sent. No server-side latest-ref lookup can silently replace a selected snapshot.

Frontend snapshot selection shows repository, role, commit, included paths and full content for review. Selecting a snapshot or editing context resets consent. Disconnection, project changes and cancelled requests discard transient token state. Generated scenarios remain unapproved.

## Implementation boundary

`internal/repository` owns the bounded GitHub and Azure DevOps Services client and concrete import types: `Input {Provider, Repository, Ref, Role, Paths}`, `File {Path, Content}`, `Snapshot {Provider, Repository, Ref, Role, CommitSHA, ContentSHA256, Files, TotalBytes}`; `New(http.RoundTripper) *Client`; `Client.Fetch(context.Context, Input, token string) (Snapshot, error)`. Export sentinel errors `ErrInvalid`, `ErrUpstream`, `ErrAccess`, `ErrTimeout`, `ErrBusy`. The client has a 45-second deadline, at most two concurrent imports, no redirects/retries, bounded response bodies, and no arbitrary network destinations.

`internal/qa` owns persistence summaries and project scoping; HTTP owns import orchestration. HTTP import request timeout is 50 seconds. Snapshot contents are stored in PostgreSQL for this local pilot. The existing 100-second server write timeout is sufficient.
