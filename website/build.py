"""Stage shared assets, then build Chinese and English with native Zensical UI."""

import argparse
from pathlib import Path
import shutil
import subprocess
import sys
import tomllib
import xml.etree.ElementTree as ET

import yaml

ROOT = Path(__file__).resolve().parent
LANGUAGES = {"zh": "zh-CN", "en": "en"}
REPOSITORY = "https://github.com/rambow-cloud/karpenter-provider-alibabacloud"


def prepare_content(staged: Path) -> tuple[str, list[str]]:
    configs = [tomllib.loads((ROOT / name).read_text(encoding="utf-8"))["project"]
               for name in ("zensical.toml", "zensical.en.toml")]
    base = configs[0]["site_url"].rstrip("/") + "/"
    if configs[1]["site_url"] != base + "en/":
        raise ValueError("English site_url must be the Chinese site_url plus en/")
    paths = {language: set((ROOT / "content" / language).rglob("*.md"))
             for language in LANGUAGES}
    routes = {language: {p.relative_to(ROOT / "content" / language).as_posix()
                         for p in files} for language, files in paths.items()}
    if routes["zh"] != routes["en"]:
        raise ValueError("Every content page needs a matching translation")
    page_routes = []
    for source_path in sorted(routes["zh"]):
        if not source_path.endswith("index.md"):
            raise ValueError(f"Use directory index pages: {source_path}")
        route = source_path.removesuffix("index.md")
        page_routes.append(route)
        for language in LANGUAGES:
            path = staged / language / source_path
            text = path.read_text(encoding="utf-8")
            if not text.startswith("---\n"):
                raise ValueError(f"Missing front matter: {path}")
            _, front_matter, content = text.split("---\n", 2)
            meta = yaml.safe_load(front_matter)
            if not meta.get("title") or not meta.get("description"):
                raise ValueError(f"Missing page title/description: {path}")
            url = base + ("en/" if language == "en" else "") + route
            graph = [{"@type": "WebPage", "@id": url + "#page", "url": url,
                      "name": meta["title"], "description": meta["description"],
                      "inLanguage": LANGUAGES[language],
                      "isPartOf": {"@id": base + "#website"}}]
            if not route:
                graph += [{"@type": "WebSite", "@id": base + "#website",
                           "url": base, "name": "Alibaba Cloud Karpenter",
                           "inLanguage": list(LANGUAGES.values())},
                          {"@type": "SoftwareSourceCode", "@id": base + "#provider",
                           "name": "Alibaba Cloud Karpenter",
                           "description": meta["description"],
                           "codeRepository": REPOSITORY, "url": base,
                           "programmingLanguage": "Go",
                           "license": "https://www.apache.org/licenses/LICENSE-2.0",
                           "isBasedOn": "https://github.com/AliyunContainerService/karpenter-provider-alibabacloud"}]
            meta["seo"] = {"root": base, "zh_url": base + route,
                           "en_url": base + "en/" + route,
                           "language": LANGUAGES[language],
                           "locale": "zh_CN" if language == "zh" else "en_US",
                           "schema": {"@context": "https://schema.org", "@graph": graph}}
            path.write_text("---\n" + yaml.safe_dump(meta, allow_unicode=True, sort_keys=False)
                            + "---\n" + content, encoding="utf-8", newline="\n")
    return base, page_routes


def write_discovery(base: str, routes: list[str]) -> None:
    namespace = "http://www.sitemaps.org/schemas/sitemap/0.9"
    xhtml = "http://www.w3.org/1999/xhtml"
    ET.register_namespace("", namespace)
    ET.register_namespace("xhtml", xhtml)
    sitemap = ET.Element(f"{{{namespace}}}urlset")
    for route in routes:
        translations = {"zh-CN": base + route, "en": base + "en/" + route,
                        "x-default": base + route}
        for language in ("zh-CN", "en"):
            entry = ET.SubElement(sitemap, f"{{{namespace}}}url")
            ET.SubElement(entry, f"{{{namespace}}}loc").text = translations[language]
            for code, url in translations.items():
                ET.SubElement(entry, f"{{{xhtml}}}link",
                              {"rel": "alternate", "hreflang": code, "href": url})
    ET.indent(sitemap)
    ET.ElementTree(sitemap).write(ROOT / "site" / "sitemap.xml", encoding="utf-8",
                                  xml_declaration=True)
    # No robots.txt: this project cannot configure the GitHub Pages host root.
    lines = ["# Alibaba Cloud Karpenter", "",
             "> Rambow Cloud's community-maintained Alibaba Cloud Karpenter provider, inspired by AWS Karpenter. A development candidate, not an official Alibaba Cloud release.", "",
             "Requires Kubernetes >=1.34; core v1.14.1 and chart 0.3.0-dev. Real ACK/ECS runtime and upgrade/rollback acceptance remain pending under issue #5. Spot interruption handling remains planned under issue #20.", "",
             "## Project documentation"]
    for route in routes:
        for language in LANGUAGES:
            path = ROOT / "content" / language / (route + "index.md")
            front = yaml.safe_load(path.read_text(encoding="utf-8").split("---\n", 2)[1])
            url = base + ("en/" if language == "en" else "") + route
            lines.append(f"- [{front['title']}]({url}): {front['description']}")
    lines += ["", "## Source and evidence", f"- [Source and license]({REPOSITORY})",
              f"- [Kubernetes policy]({REPOSITORY}/blob/main/KUBERNETES_SUPPORT.md)",
              f"- [Live issues and validation evidence]({REPOSITORY}/issues)",
              f"- [Bilingual sitemap]({base}sitemap.xml)", "",
              "This file is a navigation aid; it does not establish runtime compatibility or guarantee search/AI indexing."]
    (ROOT / "site" / "llms.txt").write_text("\n".join(lines) + "\n", encoding="utf-8", newline="\n")


def reset_directory(name: str) -> Path:
    target = (ROOT / name).resolve()
    # Recursive cleanup is restricted to these generated directories in website/.
    if target.parent != ROOT or target.name not in {".generated", "site"}:
        raise ValueError(f"Unexpected generated directory: {target}")
    if target.exists():
        shutil.rmtree(target)
    target.mkdir()
    return target


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--prepare-only", action="store_true")
    args = parser.parse_args()

    staged = reset_directory(".generated")
    for language in ("zh", "en"):
        destination = staged / language
        shutil.copytree(ROOT / "content" / language, destination)
        shutil.copytree(ROOT / "assets", destination / "assets")
    base, routes = prepare_content(staged)

    if args.prepare_only:
        return

    reset_directory("site")
    for config in ("zensical.toml", "zensical.en.toml"):
        subprocess.run(
            ["zensical", "build", "--strict", "--clean", "--config-file", config],
            cwd=ROOT,
            check=True,
        )
    (ROOT / "site" / ".nojekyll").touch()
    write_discovery(base, routes)
    subprocess.run([sys.executable, "check_site.py"], cwd=ROOT, check=True)


if __name__ == "__main__":
    main()
