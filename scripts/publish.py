#!/usr/bin/env python3
"""Publish a verified build by release ID, including resumable draft releases."""
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
from urllib.error import HTTPError
from urllib.request import Request, urlopen


def api(path: str, payload: dict | None = None, *, method: str | None = None):
    request = Request("https://api.github.com/repos/" + os.environ["GITHUB_REPOSITORY"] + path,
                      data=None if payload is None else json.dumps(payload).encode(), method=method,
                      headers={"Authorization": "Bearer " + os.environ["GH_TOKEN"],
                               "Accept": "application/vnd.github+json", "Content-Type": "application/json"})
    try:
        with urlopen(request, timeout=30) as response:
            return json.load(response)
    except HTTPError as exc:
        if exc.code == 404 and payload is None and method in (None, "GET"):
            return None
        raise


def find_release(tag: str):
    # GET /releases/tags/{tag} can return 404 for a draft. The authenticated
    # collection includes drafts; keep their stable ID for subsequent requests.
    release = api("/releases/tags/" + tag)
    if release is not None:
        return release
    page = 1
    while True:
        releases = api(f"/releases?per_page=100&page={page}")
        if not isinstance(releases, list):
            raise RuntimeError("Cannot enumerate releases")
        matches = [item for item in releases if item["tag_name"] == tag]
        if len(matches) > 1:
            raise RuntimeError("Ambiguous release tag")
        if matches:
            return matches[0]
        if len(releases) < 100:
            return None
        page += 1


def assets_match(release: dict, files: list[str]) -> bool:
    actual = {asset["name"]: asset for asset in release["assets"]}
    expected = {Path(path).name: Path(path) for path in files}
    if set(actual) != set(expected):
        return False
    for name, path in expected.items():
        if actual[name]["size"] != path.stat().st_size:
            return False
        digest = actual[name].get("digest")
        if not digest or digest != "sha256:" + hashlib.sha256(path.read_bytes()).hexdigest():
            return False
    return True


def main() -> None:
    tag, commit = os.environ["RELEASE_TAG"], os.environ["SOURCE_SHA"]
    assert re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?", tag)
    assert re.fullmatch(r"[0-9a-f]{40}", commit)
    report = json.loads(Path("dist/BUILDINFO.json").read_text())
    assert report["commit"] == commit and "v" + report["version"] == tag
    for line in Path("dist/SHA256SUMS.txt").read_text().splitlines():
        expected, name = line.split("  ", 1)
        assert Path(name).name == name, "Unsafe checksum filename"
        assert hashlib.sha256((Path("dist") / name).read_bytes()).hexdigest() == expected, "Local checksum mismatch: " + name
    existing_tag = api("/git/ref/tags/" + tag)
    if existing_tag:
        assert api("/commits/" + tag)["sha"] == commit, "Tag points to another commit; refusing to move it"
    release = find_release(tag)
    if release:
        assert release["draft"], "Published releases are immutable in this workflow"
        assert "Source commit: `" + commit + "`" in release.get("body", ""), "Unrelated draft release"
    if not existing_tag:
        obj = api("/git/tags", {"tag": tag, "message": "TryNet " + tag, "object": commit, "type": "commit"})
        api("/git/refs", {"ref": "refs/tags/" + tag, "sha": obj["sha"]})
    if not release:
        release = api("/releases", {"tag_name": tag, "target_commitish": commit, "name": "TryNet " + tag,
                                   "body": Path("dist/release-notes.md").read_text(), "draft": True, "prerelease": "-" in tag})
    release_id = release["id"]
    files = sorted(str(path) for path in Path("dist").iterdir() if path.is_file())
    if not assets_match(release, files):
        subprocess.run(["gh", "release", "upload", tag, *files, "--clobber"], check=True)
    release = api(f"/releases/{release_id}")
    assert release["draft"] and release["tag_name"] == tag
    assert assets_match(release, files), "Uploaded asset names, sizes or SHA256 digests differ from the verified build"
    published = api(f"/releases/{release_id}", {"draft": False, "make_latest": "false" if "-" in tag else "true"}, method="PATCH")
    assert not published["draft"] and published["tag_name"] == tag
    print("Published", published["html_url"])


if __name__ == "__main__":
    main()
