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
- `overrides/`: small native-theme extensions for metadata, per-page language links, and a noindex 404. No replacement theme or custom client-side rendering.
- `check_site.py`: offline checks of the generated metadata, language links, JSON-LD, local targets and complete bilingual sitemap; invoked automatically by `build.py`.
- `pyproject.toml` and `uv.lock`: pinned generator and resolved dependencies. Review upgrades in an issue and regenerate the lockfile with `uv lock`.

System fonts avoid external font requests. Search and light/dark appearance use Zensical's native components. Each language is built separately so its HTML language, search index, and interface translations agree. Goals and planned work must remain distinct from completed implementation and ACK/ECS acceptance evidence.

## Search and AI discovery

The homepage and native navigation link four independent categories: core features (`docs/features/`), quick start (`docs/quickstart/`), versions and images (`docs/support/`), and status/roadmap (`docs/status/`). Chinese/English labels and page titles agree, and previous guide routes remain available. Installation and compatibility information are readable within the site rather than only in GitHub Markdown.

The build derives breadcrumb trails from each language's explicit navigation and its section landing pages. Non-home pages display a labeled breadcrumb navigation, with the current page marked `aria-current="page"`. The same trail emits `BreadcrumbList` JSON-LD with absolute canonical URLs and sequential positions. Build checks compare visible names/links with the structured data and verify all four category destinations.

Google [sitelinks](https://developers.google.com/search/docs/appearance/sitelinks) are selected automatically. Clear categories, compact titles and descriptive internal links support discovery; neither a `BreadcrumbList` nor other schema forces sitelinks, their labels or order. [Breadcrumb markup](https://developers.google.com/search/docs/appearance/structured-data/breadcrumb) describes genuine hierarchy and is a separate search feature. Do not add a synthetic SearchAction: the local site search is not a published query-endpoint contract.

Every translated page needs a unique front-matter `title` and `description` and a matching path in the other language. The build derives absolute canonical/translation URLs from `site_url`, adds Open Graph and Twitter summary metadata, and emits WebPage JSON-LD. Homepages also identify the website and the Go source project, with repository, license and upstream attribution. Structured data must agree with visible content; do not add unverified reviews, production-readiness claims or AWS feature parity.

After both builds, `build.py` writes one root `sitemap.xml` containing all Chinese and English content pages, including reciprocal `zh-CN`, `en` and `x-default` links. No synthetic modification dates are emitted. Each page links the root sitemap; the native English-only sitemap can remain as a subset. Submit the root URL through Search Console for a property you control; deployment does not automatically submit or establish indexing.

After deployment, a maintainer can finish search-engine submission in a browser:

1. Open [Google Search Console](https://search.google.com/search-console/) and verify a URL-prefix property for `https://rambow-cloud.github.io/karpenter-provider-alibabacloud/`. Use a verification method supported by the hosting setup; add any required verification token through a separate authorized change.
2. Submit `https://rambow-cloud.github.io/karpenter-provider-alibabacloud/sitemap.xml` in Sitemaps.
3. Inspect the homepage and the four category URLs (and English equivalents), confirm crawl/index status, and request indexing where appropriate.
4. Use the [Rich Results Test](https://search.google.com/test/rich-results) on a category page to inspect BreadcrumbList. Monitor indexing and search performance; publication alone does not confirm indexing or sitelinks.

Google's [independent site names](https://developers.google.com/search/docs/appearance/site-names) apply to domain/subdomain roots, not this project subdirectory. A custom domain could provide a distinct site-name scope, but no domain, DNS or host-root configuration is changed here. This site-name restriction is separate from automated sitelinks eligibility.

The root `llms.txt` is a bilingual documentation and evidence index generated from the same content metadata. It is an optional reading aid, not a search ranking mechanism. Feature explanations, direct FAQ answers, evidence links and explicit implementation/acceptance boundaries are the main AI retrieval improvements. Google states that its AI search features use ordinary search discovery and do not require special AI files or markup: [Google AI optimization guidance](https://developers.google.com/search/docs/fundamentals/ai-optimization-guide).

This is a GitHub Pages **project site** under `/karpenter-provider-alibabacloud/`. Crawlers read robots.txt at the **host root**, not the project subpath; this repository cannot set `https://rambow-cloud.github.io/robots.txt`. We therefore do not publish a misleading project-local robots.txt. Host-wide crawler rules would require an authorized change to the organization Pages repository. The HTML robots metadata permits indexing for content pages and marks both 404 pages noindex. See [Google robots.txt guidance](https://developers.google.com/search/docs/crawling-indexing/robots/intro).

## Publication and review

Website changes follow issue → `feat/<issue-number>-<description>` → PR, using the repository PR template. `.github/workflows/website.yaml` builds on website PRs and pushes to `main`; only `main` deployments have Pages write permissions. Actions and tool versions are pinned. A successful PR build does not publish the site. Merging triggers a production build and the GitHub Pages deployment.

The repository Pages source is **GitHub Actions**. No custom domain is needed. If the repository URL changes, update both `site_url` values and language-selector links.

After deployment succeeds, open these in a browser (use Ctrl+F5 if the previous site is cached):

- Chinese: https://rambow-cloud.github.io/karpenter-provider-alibabacloud/
- English: https://rambow-cloud.github.io/karpenter-provider-alibabacloud/en/
- Guide: https://rambow-cloud.github.io/karpenter-provider-alibabacloud/docs/motivation/

Check the Rambow + K logo, desktop/mobile navigation, language selector, light/dark toggle, and search. This browser review is separate from the strict build and deployment status; neither establishes real ACK/ECS acceptance.

Check the compact brand mark, four category links and six feature cards at desktop and phone widths. Open `/docs/quickstart/` and `/docs/support/`, follow the visible breadcrumbs and switch to English: the language selector should stay on the matching page. Also open `/sitemap.xml` (16 content URLs at this change) and `/llms.txt`. These are production URLs under the same project prefix. Automated builds check artifacts; visual browser review remains with the maintainer as agreed.

To roll back a website release, revert its PR through a new PR. After the required checks pass and it merges, the website workflow republishes the reverted source. Do not manually edit the deployed artifact.

Provider source retains its Apache-2.0 license and AliyunContainerService attribution. Zensical is MIT-licensed and supplies its own native theme and assets. Branding is supplied by Rambow Cloud and is independent of provider code licensing.
