# Contributing

Start with an issue using the bug, feature, or compatibility form. Record the problem, scope, acceptance criteria, and validation plan before implementation. Maintainers remove `needs-triage`, assign a milestone, and add the issue to the repository project.

Create one feature branch per primary issue:

```sh
git switch main
git pull --ff-only origin main
git switch -c feat/123-karpenter-compatibility
```

Use `feat/<issue-number>-<short-description>` for all implementation work, including fixes and tooling. Use a conventional PR title such as `feat: support Karpenter v1.14.1` or `fix: scope instance discovery to the current cluster`. Fill the PR template and include `Closes #123` for the primary issue. Additional issues may be linked in the same PR when their acceptance criteria are completed by that change.

Descriptions use lowercase letters and digits separated by hyphens; dots are also allowed for pinned version numbers, for example `feat/123-karpenter-v1.14.1`.

PR policy checks validate the branch, title, closing reference, and required sections. Fork PRs run with read-only permissions. Squash merging keeps one logical change per PR; GitHub closes linked issues when the PR reaches the default branch.

The main branch requires a pull request, linear history, and the `policy` check. The new `validation` job must also pass for compatibility changes; add it to required branch checks after the CI workflow reaches main. Stacked PRs may target their dependency branch until the preceding PR is merged.

The project uses Todo, In Progress, and Done. Move an issue to In Progress when its feature branch is created. Keep it there while its PR is open. Move it to Done only after the PR is merged and required validation is complete. The exact upstream stable version is pinned in each compatibility milestone; later releases receive a separate issue and milestone.

Run checks relevant to your change once. Local unit checks: `go test ./pkg/... ./test/hack/...`; controller suites additionally require Linux envtest assets. Run `go vet ./pkg/... ./cmd/...`, `go build ./cmd/controller`, `helm lint charts/karpenter`, and `make e2e-parity` when applicable. Cloud E2E is opt-in and requires an explicitly selected test ACK cluster. Record unavailable checks instead of claiming they passed.

Do not commit credentials, kubeconfigs, account identifiers, or unsanitized cluster logs.

Build and test with the security-patched toolchain pinned in `go.mod`; CGO defaults to disabled. Dependency changes must pass `go run ./hack/security` and the CI vulnerability scans. The checker reads effective replacement versions and allows a module to be removed. See [SECURITY_AUDIT.md](SECURITY_AUDIT.md) for audit scope, reproduction commands and remaining unused-package advisories. CGO-enabled race tests require an explicit override and a C compiler.

## Project website

Website source lives in `website/` and uses Zensical with locked Python dependencies. Follow the same issue, feature-branch, and PR workflow for website changes. Read [website/README.md](website/README.md) for the website toolchain, strict build command, GitHub Pages publication, and rollback. Keep Chinese and English copy consistent and distinguish project goals from completed implementation and cloud acceptance evidence.

