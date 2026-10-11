# GitHub Actions versions

External actions are pinned to complete commit SHAs. The comments beside `uses:` record the corresponding stable release. The official latest release and its tag's commit were verified on **2026-10-12**.

| Action | Stable release | Pinned commit |
| --- | --- | --- |
| actions/checkout | [v7.0.1](https://github.com/actions/checkout/releases/tag/v7.0.1) | `3d3c42e5aac5ba805825da76410c181273ba90b1` |
| actions/setup-go | [v7.0.0](https://github.com/actions/setup-go/releases/tag/v7.0.0) | `b7ad1dad31e06c5925ef5d2fc7ad053ef454303e` |
| actions/upload-artifact | [v7.0.2](https://github.com/actions/upload-artifact/releases/tag/v7.0.2) | `cf430e030ddbb5b0abf93d22962f4752f3646cd9` |
| azure/setup-helm | [v5.0.1](https://github.com/Azure/setup-helm/releases/tag/v5.0.1) | `9bc31f4ebc9c6b171d7bfbaa5d006ae7abdb4310` |
| helm/kind-action | [v1.15.1](https://github.com/helm/kind-action/releases/tag/v1.15.1) | `1544676c07bb3570fcb70166beaa07c4528fd4aa` |
| astral-sh/setup-uv | [v10.3.0](https://github.com/astral-sh/setup-uv/releases/tag/v10.3.0) | `1c37ad07a6a961277cf70c0d37d6f313000f5884` |
| actions/configure-pages | [v6.0.0](https://github.com/actions/configure-pages/releases/tag/v6.0.0) | `45bfe0192ca1faeb007ade9deae92b16b8254a0d` |
| actions/upload-pages-artifact | [v5.0.0](https://github.com/actions/upload-pages-artifact/releases/tag/v5.0.0) | `fc324d3547104276b827a68afc52ff2a11cc49c9` |
| actions/deploy-pages | [v5.0.1](https://github.com/actions/deploy-pages/releases/tag/v5.0.1) | `368f82528645a54fb793d4d04e342629a3f51346` |

The upgraded JavaScript actions use Node 24 on the repository's GitHub-hosted `ubuntu-latest` runners. Existing workflow inputs remain supported. Action releases and installed tool versions are separate: this update preserves the pinned Go, Helm, kind, Kubernetes, uv and Python versions and the 10-pod KWOK smoke default.

Updates limited to `.github/` use the existing lightweight routing: Python routing tests and actionlint run; Go setup/build/tests/security scans, E2E parity compilation and KWOK cluster creation are skipped. A change to the website workflow also runs its existing bilingual website build. The cloud workflows remain opt-in. See [CONTRIBUTING.md](../CONTRIBUTING.md#ci-scope-and-lightweight-updates).
