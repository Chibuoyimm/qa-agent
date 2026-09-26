# Repository context contract

Repository context is a bounded, immutable source snapshot for the existing single-operator pilot. It informs proposals; it is not proof of deployed behaviour or an independent business oracle. Human expectations still take precedence over implementation-derived values.

## Import and review

`POST /api/projects/{id}/repositories/sync` accepts `{repository: "owner/name", ref: "main", role: "frontend"|"backend", paths: string[]}`. `paths` contains 1–20 exact repository-relative text file paths; the operator explicitly chooses what may be imported. Optional `X-QA-GitHub-Token` is transient and used only against the fixed GitHub API origin. No token is stored, logged, or forwarded to the model. Public repositories work without a token. Private repositories require a read-capable token for each sync.

The importer resolves the ref to a commit SHA, reads all requested files at that commit, and returns errors rather than silently dropping or truncating files. Only regular text blobs are accepted, no symlinks/submodules. Reject environment/key/credential files, binary content, recognizable embedded secrets, unsafe paths, and oversized content. These checks are an aid, not a guarantee that source contains no sensitive material; review the complete imported content before consenting to send it to a provider. Limit to 40,000 bytes of file content per snapshot and 60,000 bytes of assembled proposal context. Import never executes repository code.

`GET /api/projects/{id}/repositories` returns at most 100 newest snapshot summaries as an array. Summary fields: `id`, `project_id`, `repository`, `ref`, `role`, `commit_sha`, `content_sha256`, `file_count`, `total_bytes`, `created_at`.

`GET /api/projects/{id}/repositories/{snapshot_id}` returns the summary plus `files: [{path, content}]`. Creating a new snapshot does not mutate earlier records. Re-sync uses the same form and produces a new snapshot pinned to the newly resolved commit. The initial UI supports deliberate sync; automatic webhook sync comes after this contract.

## Proposals

Existing proposal input gains optional `repository_snapshot_ids: string[]` (at most two, unique, same project, at most one per role). `context` may be empty only when snapshots are selected. The server builds one bounded context from operator context and snapshot headers/files; validation and consent apply to that final text. The response adds `repository_snapshots`, containing the selected summaries. The existing `context_sha256` hashes the complete context actually sent. No server-side latest-ref lookup can silently replace a selected snapshot.

Frontend snapshot selection shows repository, role, commit, included paths and full content for review. Selecting a snapshot or editing context resets consent. Disconnection, project changes and cancelled requests discard transient token state. Generated scenarios remain unapproved.

## Implementation boundary

`internal/repository` owns the GitHub client and concrete import types: `Input {Repository, Ref, Role, Paths}`, `File {Path, Content}`, `Snapshot {Repository, Ref, Role, CommitSHA, ContentSHA256, Files, TotalBytes}`; `New(http.RoundTripper) *Client`; `Client.Fetch(context.Context, Input, token string) (Snapshot, error)`. Export sentinel errors `ErrInvalid`, `ErrUpstream`, `ErrTimeout`, `ErrBusy`. The client has a 45-second deadline, at most two concurrent imports, no redirects/retries, bounded response bodies, and no arbitrary network destinations.

`internal/qa` owns persistence summaries and project scoping; HTTP owns import orchestration. HTTP import request timeout is 50 seconds. Snapshot contents are stored in PostgreSQL for this local pilot. The existing 100-second server write timeout is sufficient.
