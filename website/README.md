# Project website

The project introduction and guide use [Zensical](https://zensical.org/) **0.0.69** with the native modern theme. Python **3.14.2** and uv **0.12.21** are used in CI; `uv.lock` locks the website's Python dependencies independently of the Go controller.

Chinese is published at https://rambow-cloud.github.io/karpenter-provider-alibabacloud/ and English at `/en/`. Both languages preserve the previous `docs/motivation/`, `docs/status/`, and `docs/participate/` URLs.

## Build

Install uv, then run from this directory:

```sh
uv sync --frozen --python 3.14.2
uv run --frozen python build.py
```

The script stages each language in `.generated/` with a copy of the shared `assets/`, then runs two clean, strict Zensical builds. Chinese writes `site/`; English writes `site/en/`. Cleanup is restricted to the generated `.generated/` and `site/` directories under `website/`. The Pages artifact is `site/`; generated assets, caches and the virtual environment are ignored by Git.

For a Chinese local preview:

```sh
uv run --frozen python build.py --prepare-only
uv run --frozen zensical serve --config-file zensical.toml
```

Use `zensical.en.toml` instead for the English preview. Language-selector links intentionally point to the production Pages subpath; use separate preview commands to inspect each language locally. Restage after changing shared assets or source content.

## Editing

- `content/zh/` and `content/en/`: translated Markdown; edit both together.
- `zensical.toml` and `zensical.en.toml`: navigation, site language, Pages URL, and theme settings.
- `assets/brand.css`: small branding adjustments; keep the native layout and responsive navigation.
- `assets/rambow-karpenter.png`: transparent Rambow ram/cloud + K logo. Original input and edit provenance are in [branding/README.md](branding/README.md).
- `pyproject.toml` and `uv.lock`: pinned generator and resolved dependencies. Review upgrades in an issue and regenerate the lockfile with `uv lock`.

System fonts avoid external font requests. Search and light/dark appearance use Zensical's native components. Each language is built separately so its HTML language, search index, and interface translations agree. Goals and planned work must remain distinct from completed implementation and ACK/ECS acceptance evidence.

## Publication and review

Website changes follow issue → `feat/<issue-number>-<description>` → PR, using the repository PR template. `.github/workflows/website.yaml` builds on website PRs and pushes to `main`; only `main` deployments have Pages write permissions. Actions and tool versions are pinned. A successful PR build does not publish the site. Merging triggers a production build and the GitHub Pages deployment.

The repository Pages source is **GitHub Actions**. No custom domain is needed. If the repository URL changes, update both `site_url` values and language-selector links.

After deployment succeeds, open these in a browser (use Ctrl+F5 if the previous site is cached):

- Chinese: https://rambow-cloud.github.io/karpenter-provider-alibabacloud/
- English: https://rambow-cloud.github.io/karpenter-provider-alibabacloud/en/
- Guide: https://rambow-cloud.github.io/karpenter-provider-alibabacloud/docs/motivation/

Check the Rambow + K logo, desktop/mobile navigation, language selector, light/dark toggle, and search. This browser review is separate from the strict build and deployment status; neither establishes real ACK/ECS acceptance.

To roll back a website release, revert its PR through a new PR. After the required checks pass and it merges, the website workflow republishes the reverted source. Do not manually edit the deployed artifact.

Provider source retains its Apache-2.0 license and AliyunContainerService attribution. Zensical is MIT-licensed and supplies its own native theme and assets. Branding is supplied by Rambow Cloud and is independent of provider code licensing.
