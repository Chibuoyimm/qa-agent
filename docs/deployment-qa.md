# Deployment-triggered QA

A release links a deployment URL, the frontend/backend source revisions, immutable repository snapshots, and one run of approved scenarios. The release URL must be an exact origin in `QA_ALLOWED_ORIGINS` on both API and worker. Creating a release does not change the project's default URL.

## Pipeline command

`qa release --manifest release.json --timeout 10m --poll 1s --json`

The manifest is strict JSON, at most 64 KiB:

```json
{
  "project_id": "PROJECT_ID",
  "deployment_key": "production-12345",
  "base_url": "https://staging.example.com",
  "readiness_path": "/healthz",
  "mode": "blocking",
  "scenario_ids": ["APPROVED_SCENARIO_ID"],
  "repositories": [
    {
      "repository": "owner/frontend",
      "role": "frontend",
      "commit_sha": "0123456789012345678901234567890123456789",
      "paths": ["src/dashboard.tsx"]
    },
    {
      "repository": "owner/backend",
      "role": "backend",
      "commit_sha": "abcdefabcdefabcdefabcdefabcdefabcdefabcd",
      "paths": ["internal/revenue/revenue.go"]
    }
  ]
}
```

Use a stable deployment key across job retries, with letters, digits, underscores, and hyphens (1–200 characters). Do not include the retry attempt number. Use a new key for a new deployment or an intentionally new execution. Select 1–50 unique approved scenarios and 1–2 repositories with distinct `frontend`/`backend` roles. Each revision must be a full 40-character hexadecimal commit SHA; each import requires 1–20 explicitly selected safe source paths. Credentials belong in `QA_API_TOKEN` and optional `QA_GITHUB_TOKEN`, never in the manifest. `QA_API_BASE_URL` identifies the QA API, separately from the deployment URL.

The command first looks up the deployment key. An existing matching release resumes polling the same run without readiness checks or repository imports. A changed target, mode, ordered scenario selection, repository revision, or selected paths under the same key is an error. A new release waits for HTTP 200 from the readiness path, imports the exact commits, creates the release, and waits for its run. Readiness requests carry no QA/GitHub credentials and do not follow redirects. The overall timeout covers readiness, imports, creation, and polling. Timeout does not cancel a remote run; retry with the same manifest to resume.

API responses are limited to 4 MiB in the command; very large scenario definitions that exceed that response limit return a client error. A failed attempt before release creation can leave imported snapshots, but cannot leave an unrecorded queued run. Concurrent attempts may each import sources before one wins release creation; only one run is created. Retries after creation reuse the saved snapshots as well.

Exit 0 means pass (or a warning in advisory mode); exit 1 means a failed blocking gate; exit 2 means invalid configuration, transport failure, timeout, or an inconsistent API result. With `--json`, stdout contains one terminal release JSON object; progress goes to stderr.

The supplied commits record what the pipeline says it deployed. HTTP readiness alone does not verify the deployed application's build identity. Deploy jobs must supply their actual built revisions; applications can separately expose and verify build metadata before this command.

## GitHub Actions setup

Copy [the reusable workflow](examples/deployment-qa.yml) to `.github/workflows/deployment-qa.yml` in the application repository. Run it after your deploy job, passing that job's actual built revisions and deployment URL. Pin `qa_revision` to a reviewed QA Agent commit. The API and a running browser worker must be reachable and configured with the deployment's exact origin, approved scenarios, and worker-local test credentials. A private target needs a runner with suitable network access.

For example, add this job alongside an existing `deploy` job:

```yaml
qa:
  needs: deploy
  uses: ./.github/workflows/deployment-qa.yml
  with:
    qa_revision: REVIEWED_QA_AGENT_FULL_COMMIT_SHA
    qa_api_url: ${{ vars.QA_API_BASE_URL }}
    project_id: ${{ vars.QA_PROJECT_ID }}
    deployment_key: staging-${{ github.run_id }}
    deployment_url: ${{ needs.deploy.outputs.url }}
    scenario_ids: ${{ vars.QA_SCENARIO_IDS_JSON }}
    frontend_repository: ${{ github.repository }}
    frontend_commit_sha: ${{ needs.deploy.outputs.frontend_sha }}
    frontend_paths: '["src/dashboard.tsx"]'
    backend_repository: ${{ github.repository }}
    backend_commit_sha: ${{ needs.deploy.outputs.backend_sha }}
    backend_paths: '["internal/revenue/revenue.go"]'
    mode: blocking
  secrets:
    QA_API_TOKEN: ${{ secrets.QA_API_TOKEN }}
    QA_GITHUB_TOKEN: ${{ secrets.QA_GITHUB_TOKEN }}
```

Replace paths with your reviewed files; omit all backend inputs for a frontend-only release. The example uses `github.run_id` because it remains unchanged on reruns; do not use `github.run_attempt` in the deployment key. [GitHub's context reference](https://docs.github.com/en/enterprise-cloud%40latest/actions/reference/workflows-and-actions/contexts) documents both values. For multiple deployments in one workflow run, include an environment or other stable deployment identifier in the key. GitHub marks a failed blocking gate as a failed job, and saves the terminal release JSON as an artifact. Advisory mode returns a successful job with a warning in the result.

## API contract

All routes require the operator API token:

- `POST /api/projects/{projectID}/releases` accepts `{deployment_key, base_url, mode, scenario_ids, repositories: [{snapshot_id, repository, role, commit_sha}]}` and returns HTTP 202, including on an identical retry
- `GET /api/projects/{projectID}/releases` returns the latest 100 releases
- `GET /api/projects/{projectID}/releases/by-key/{deploymentKey}` returns the matching release, or 404
- `GET /api/projects/{projectID}/releases/{releaseID}` returns release details scoped to the project

A release response has `id`, `project_id`, `deployment_key`, `base_url`, `mode`, `repositories`, `run`, and `created_at`. Each stored repository entry has `snapshot_id`, `repository`, `role`, `commit_sha`, `content_sha256`, and sorted `paths`. `run` is the existing full run object, including frozen scenarios and current results. No source content or credentials are added to release records.

The server verifies every snapshot belongs to the project and agrees with the supplied repository, role, and commit. The release and its run are created in one database transaction. The unique project/deployment key serializes concurrent requests: matching requests return the original release/run; changed inputs return 409. Semantic identity includes source content hashes and paths, rather than snapshot IDs, so re-importing the same sources during a race remains an identical request. Scenario order remains meaningful and is preserved. Invalid inputs or unapproved scenarios create neither a release nor a run.

## Review and validation

The Releases page shows the target, frontend/backend commits, snapshot links, gate, run ID, scenario expectations, and results. It polls active releases and offers a manual refresh after errors.

Verification must exercise a healthy deployment passing, a deliberately incorrect dashboard failing the blocking gate, the run-specific target differing from the project's default, repeated and concurrent requests producing one run, changed-key-content rejection, wrong-project sources, unapproved scenarios, denied origins, CLI readiness/import/polling, and desktop/mobile release details. Use the existing disposable integration harness and preserve its cleanup guarantees.
