# Project website

The project introduction is published at <https://rambow-cloud.github.io/karpenter-provider-alibabacloud/>. Simplified Chinese is at the root; English is at `/en/`. The site is tracked by [Issue #30](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/30) and the [website launch milestone](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/milestone/6).

## Toolchain

- Hugo **Extended 0.167.0**
- Go **1.27.1** for website tooling
- [OINK **v1.2.0**](https://github.com/pgsty/oink), pinned in this directory's separate `go.mod` / `go.sum`

Run Hugo from `website/`. The controller's root Go module is independent. No Node.js, npm, analytics, or comment service is required.

## Edit and build

`data/home/zh.yaml` and `data/home/en.yaml` contain the native OINK landing content. Translated guides live side by side in `content/docs/` with explicit `.zh.md` and `.en.md` suffixes. Keep both languages consistent, and distinguish project goals, merged implementation, offline evidence, and pending cloud acceptance.

```sh
cd website
go mod download
hugo --cleanDestinationDir --gc --minify --environment production --printPathWarnings --panicOnWarning
```

For local preview:

```sh
hugo server
```

Open the URL printed by Hugo in a browser. Generated `public/`, `resources/`, caches, and build locks are ignored. Theme upgrades are deliberate changes to the website module; update the lockfile and run the strict build once after changing inputs.

## Publish and roll back

`.github/workflows/website.yaml` builds website PRs with read-only permissions. After merge to `main`, or a manual workflow run on `main`, it builds and deploys through the GitHub Pages artifact API. The deployment job alone receives `pages: write` and `id-token: write`; a PR cannot deploy. Tool releases are fixed, actions are pinned to commits, and Hugo downloads are checksum-verified.

Repository Settings → Pages must use **GitHub Actions** as the source. The production base URL is taken from `actions/configure-pages`, with the canonical project subpath also present in `hugo.yaml`. No custom DNS configuration is needed.

To roll back, revert the relevant website commit through a PR and merge it to `main`. A manual deployment rebuilds current `main`; it does not deploy an arbitrary historical commit.

The GitHub Actions deployment result confirms publication. Browser appearance is a separate visual check: open the Chinese and English homepages, a guide page, and try mobile navigation, search, and the dark-mode toggle. Do not substitute command-line HTTP checks for that review.

## Attribution

The provider and site content remain under the repository's Apache-2.0 license. OINK is an Apache-2.0 theme derived from Docsy; its own NOTICE and third-party vendor notices remain in the pinned theme module. The project motivation page links to the official Alibaba Cloud provider, AWS provider, OINK, and Hugo.
