# Contributing

Start with an issue using the bug, feature, or compatibility form. Record the problem, scope, acceptance criteria, and validation plan before implementation. Maintainers remove `needs-triage`, assign a milestone, and add the issue to the repository project.

Create one feature branch per primary issue:

```sh
git switch dev
git pull --ff-only origin dev
git switch -c feat/123-karpenter-compatibility
```

Use `feat/<issue-number>-<short-description>` for all implementation work, including fixes and tooling, and target **dev**. Prefer one issue per PR. Use a conventional PR title such as `feat: support Karpenter v1.14.1` or `fix: scope instance discovery to the current cluster`. Fill the PR template and include `Closes #123` for the primary issue.

Descriptions use lowercase letters and digits separated by hyphens; dots are also allowed for pinned version numbers, for example `feat/123-karpenter-v1.14.1`.

PR policy checks validate the branch, target, title, closing reference, and required sections. Fork PRs run with read-only permissions. Squash merging keeps one logical change per PR. GitHub only automatically closes linked issues on the default branch; after a reviewed PR passes its required checks and merges into dev, explicitly close its issue as completed and record the PR, dev commit, checks and any remaining release gates. Project Done means that issue's acceptance criteria are complete in dev; it does not claim main delivery or ACK runtime acceptance.

The main branch requires a pull request, linear history, and the `policy` and `validation` checks. Implementation PRs target dev. Once the development batch is complete, rebase dev onto the latest main, resolve conflicts and validate the resulting commit before opening a **dev -> main** integration PR. Preserve main history and use the repository's existing squash merge policy for final delivery. Coordinate any required dev force push and use `--force-with-lease`.

The current main checkpoint contains completed implementation issues #15, #16, #18, #29, #51, #52, #67, #74, #76, #79 and #81. Integration policy requires each tracked issue to be closed as **completed**; API errors and not-planned closures still block integration. On 2026-10-12 the maintainer requested integrating this accepted checkpoint and then running bounded KWOK, before deferred live ACK acceptance. #5/#23 remain separate release/ACK gates, and #20 remains an independent Spot feature. They are not closed or accepted by a main merge or KWOK result. A main checkpoint is a development candidate, not a stable release or proof of actual provider behavior on ACK. See `.github/scripts/pr_policy.py` when defining the next accepted implementation batch.

The project uses Todo, In Progress, and Done. Move an issue to In Progress when its feature branch is created. Keep it there while its PR is open. Move it to Done only after the PR is merged and required validation is complete. The exact upstream stable version is pinned in each compatibility milestone; later releases receive a separate issue and milestone.

Run checks relevant to your change once. Local unit checks: `go test ./pkg/... ./test/hack/...`; controller suites additionally require Linux envtest assets. Run `go vet ./pkg/... ./cmd/...`, `go build ./cmd/controller`, `helm lint charts/karpenter`, and `make e2e-parity` when applicable. Cloud E2E is opt-in and requires an explicitly selected test ACK cluster. Record unavailable checks instead of claiming they passed.

Do not commit credentials, kubeconfigs, account identifiers, or unsanitized cluster logs.

## CI scope and lightweight updates

Website, documentation and GitHub Actions changes use a lightweight path. The `validation` job always runs the shared Python change-routing checks and pinned, SHA256-verified actionlint binary. It does not install Go/Helm/envtest, compile or test Go, run vet/tidy, generate deployment artifacts, or scan Go vulnerabilities when all changed files are in these categories:

- `website/**` and `docs/**`;
- Markdown (`*.md`) and reStructuredText (`*.rst`) documents;
- `.github/**`, including workflow/action configuration, CI scripts and issue/PR templates.

Any `*.go`, `go.mod`, `go.sum`, `go.work` or `go.work.sum` change overrides the lightweight rule even inside those directories. Build files (such as Makefile/Dockerfile), charts, non-documentation test fixtures and unknown paths retain full Go validation. Mixed changes retain the full path. Routing uses the entire PR merge-base diff or push commit range, including deleted paths and both sides of renames. Missing comparison commits conservatively retain full validation. Issue labels and commit titles do not control skipping.

All E2E workflows, including KWOK and E2EParity, require explicit manual dispatch. They do not run on PRs, pushes, schedules or chained completion events. Reusable E2E jobs reject any event other than `workflow_dispatch`; ACK creation also requires `ACK_E2E_ENABLED=true`. Manual cleanup remains available when that variable is false. Keep ordinary CI/unit/contract validation separate from live E2E execution.

Explicit manual E2E dispatch retains the selected test execution. KWOK still uses its dedicated `run_kwok` gate; source classification is not an automatic trigger. The KWOK job builds its pinned core controller as needed; `run_go` refers to provider validation. PR policy and ordinary CI checks remain; website changes keep the strict bilingual build and Pages deployment. Required CI workflows are not disabled with paths-ignore or skip-ci commits, which could leave required checks pending: [GitHub required-check guidance](https://docs.github.com/en/pull-requests/how-tos/merge-and-close-pull-requests/troubleshooting-required-status-checks).

Use the **Website or documentation / 网站与文档** issue form for content work. GitHub Actions changes may use the feature form. Follow the same issue → feat branch → PR process; review the `Validation scope` Actions summary for the selected path. To check routing/workflow changes locally, use Python and an existing or downloaded pinned actionlint binary; do not compile Go just to validate these configuration-only changes.

仅更新网站、文档或 GitHub Actions 配置时，保留轻量校验，跳过 Go 安装、编译、测试、依赖整理和安全扫描。混合修改 Go、依赖、构建文件或其他未归类文件时仍运行完整 CI；手动触发保持完整验证。路由按真实文件变更判断，与 Issue 标签无关。

Build and test with the security-patched toolchain pinned in `go.mod`; CGO defaults to disabled. Dependency changes must pass `go run ./hack/security` and the CI vulnerability scans. The checker reads effective replacement versions and allows a module to be removed. See [SECURITY_AUDIT.md](SECURITY_AUDIT.md) for audit scope, reproduction commands and remaining unused-package advisories. CGO-enabled race tests require an explicit override and a C compiler.

## Project website

Website source lives in `website/` and uses Zensical with locked Python dependencies. Follow the same issue, feature-branch, and PR workflow for website changes. Read [website/README.md](website/README.md) for the website toolchain, strict build command, GitHub Pages publication, and rollback. Keep Chinese and English copy consistent and distinguish project goals from completed implementation and cloud acceptance evidence.

