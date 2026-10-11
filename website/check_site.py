"""Check generated search metadata, translation links and local targets offline."""

from html.parser import HTMLParser
import json
from pathlib import Path
import tomllib
from urllib.parse import unquote, urlsplit
import xml.etree.ElementTree as ET

ROOT = Path(__file__).resolve().parent
SITE = ROOT / "site"
BASE = tomllib.loads((ROOT / "zensical.toml").read_text(encoding="utf-8"))["project"]["site_url"]
NS = {"s": "http://www.sitemaps.org/schemas/sitemap/0.9",
      "x": "http://www.w3.org/1999/xhtml"}


class Page(HTMLParser):
    def __init__(self, text: str):
        super().__init__()
        self.metas = {}
        self.links = []
        self.targets = []
        self.scripts = []
        self.language = None
        self.h1 = 0
        self.title = ""
        self.in_title = False
        self.in_schema = False
        self.feed(text)

    def handle_starttag(self, tag, attrs):
        attrs = dict(attrs)
        if tag == "html":
            self.language = attrs.get("lang")
        if tag == "meta":
            key = attrs.get("name", attrs.get("property"))
            self.metas.setdefault(key, []).append(attrs.get("content"))
        if tag == "link":
            self.links.append(attrs)
        if tag in {"a", "link", "img", "script"}:
            self.targets.append(attrs.get("href", attrs.get("src", "")))
        if tag == "h1":
            self.h1 += 1
        if tag == "title":
            self.in_title = True
        if tag == "script" and attrs.get("type") == "application/ld+json":
            self.in_schema = True
            self.scripts.append("")

    def handle_endtag(self, tag):
        if tag == "title":
            self.in_title = False
        if tag == "script":
            self.in_schema = False

    def handle_data(self, data):
        if self.in_title:
            self.title += data
        if self.in_schema:
            self.scripts[-1] += data


def local_target(url: str, source: Path) -> Path | None:
    parsed = urlsplit(url)
    if not parsed.path:
        return None
    if parsed.scheme or parsed.netloc:
        if not url.startswith(BASE):
            return None
        path = SITE / unquote(parsed.path.removeprefix(urlsplit(BASE).path))
    elif parsed.path.startswith("/"):
        if not parsed.path.startswith(urlsplit(BASE).path):
            raise ValueError(f"Link escapes Pages project: {url}")
        path = SITE / unquote(parsed.path.removeprefix(urlsplit(BASE).path))
    else:
        path = source.parent / unquote(parsed.path)
    if parsed.path.endswith("/") or path.is_dir():
        path /= "index.html"
    return path


def main():
    entries = ET.parse(SITE / "sitemap.xml").getroot().findall("s:url", NS)
    locations = [entry.findtext("s:loc", namespaces=NS) for entry in entries]
    expected = set()
    descriptions = set()
    for language in ("zh", "en"):
        for source in sorted((ROOT / "content" / language).rglob("index.md")):
            route = source.relative_to(ROOT / "content" / language).as_posix().removesuffix("index.md")
            prefix = "en/" if language == "en" else ""
            canonical = BASE + prefix + route
            expected.add(canonical)
            path = SITE / prefix / route / "index.html"
            html = path.read_text(encoding="utf-8")
            page = Page(html)
            assert page.language == language, (path, page.language)
            assert page.h1 == 1 and page.title.strip(), f"Missing/duplicate heading or title: {path}"
            description = page.metas.get("description", [])
            assert len(description) == 1 and description[0], f"Missing/duplicate description: {path}"
            assert description[0] not in descriptions, f"Repeated description: {path}"
            descriptions.add(description[0])
            assert [link.get("href") for link in page.links if link.get("rel") == "canonical"] == [canonical], path
            translations = {"zh-CN": BASE + route, "en": BASE + "en/" + route,
                            "x-default": BASE + route}
            alternate = [link for link in page.links if link.get("hreflang")]
            assert len(alternate) == 3, f"Duplicate/missing hreflang: {path}"
            assert {link["hreflang"]: link["href"] for link in alternate} == translations, path
            assert page.metas.get("og:url") == [canonical], path
            assert page.metas.get("og:description") == description, path
            assert page.metas.get("twitter:description") == description, path
            assert page.metas.get("robots") == ["index,follow,max-image-preview:large"], path
            assert len(page.scripts) == 1, f"Missing/duplicate JSON-LD: {path}"
            schema = json.loads(page.scripts[0])
            assert schema["@context"] == "https://schema.org", path
            webpage = schema["@graph"][0]
            assert webpage["url"] == canonical and webpage["description"] == description[0], path
            assert webpage["inLanguage"] == ("en" if language == "en" else "zh-CN"), path
            for url in (translations["zh-CN"], translations["en"]):
                assert f'href="{url}" hreflang=' in html, path
            for url in page.targets:
                target = local_target(url, path)
                if target is not None:
                    assert target.is_file(), f"Missing target {url} from {path}"
    assert len(locations) == len(set(locations)) and set(locations) == expected, "Sitemap content mismatch"
    for entry in entries:
        url = entry.findtext("s:loc", namespaces=NS)
        route = url.removeprefix(BASE).removeprefix("en/")
        links = entry.findall("x:link", NS)
        assert len(links) == 3
        assert {link.attrib["hreflang"]: link.attrib["href"] for link in links} == {
            "zh-CN": BASE + route, "en": BASE + "en/" + route, "x-default": BASE + route}
    for prefix in ("", "en/"):
        error = Page((SITE / prefix / "404.html").read_text(encoding="utf-8"))
        assert error.metas.get("robots") == ["noindex,follow"]
    llms = (SITE / "llms.txt").read_text(encoding="utf-8")
    for url in expected:
        assert url in llms, f"Missing AI navigation URL: {url}"
    print(f"Website metadata and discovery checks passed: {len(expected)} bilingual pages")


if __name__ == "__main__":
    main()
