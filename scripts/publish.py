#!/usr/bin/env python3
"""Publish only a fully verified build, without replacing a published release."""
import json
import os
from pathlib import Path
import re
import subprocess
from urllib.error import HTTPError
from urllib.request import Request, urlopen


def api(path: str, payload: dict | None = None):
    request = Request("https://api.github.com/repos/" + os.environ["GITHUB_REPOSITORY"] + path,
                      data=None if payload is None else json.dumps(payload).encode(),
                      headers={"Authorization": "Bearer " + os.environ["GH_TOKEN"],
                               "Accept": "application/vnd.github+json", "Content-Type": "application/json"})
    try:
        with urlopen(request, timeout=30) as response:
            return json.load(response)
    except HTTPError as exc:
        if exc.code == 404 and payload is None:
            return None
        raise


def main() -> None:
    tag, commit = os.environ["RELEASE_TAG"], os.environ["SOURCE_SHA"]
    assert re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?", tag)
    assert re.fullmatch(r"[0-9a-f]{40}", commit)
    report = json.loads(Path("dist/BUILDINFO.json").read_text())
    assert report["commit"] == commit and "v" + report["version"] == tag
    existing_tag = api("/git/ref/tags/" + tag)
    if existing_tag:
        assert api("/commits/" + tag)["sha"] == commit, "Tag points to another commit; refusing to move it"
    existing_release = api("/releases/tags/" + tag)
    if existing_release:
        assert existing_release["draft"], "Published releases are immutable in this workflow"
        assert "Source commit: `" + commit + "`" in existing_release.get("body", ""), "Unrelated draft release"
    if not existing_tag:
        obj = api("/git/tags", {"tag": tag, "message": "TryNet " + tag, "object": commit, "type": "commit"})
        api("/git/refs", {"ref": "refs/tags/" + tag, "sha": obj["sha"]})
    files = sorted(str(path) for path in Path("dist").iterdir() if path.is_file())
    if existing_release:
        subprocess.run(["gh", "release", "upload", tag, *files, "--clobber"], check=True)
    else:
        subprocess.run(["gh", "release", "create", tag, *files, "--draft", "--verify-tag", "--title", "TryNet " + tag,
                        "--notes-file", "dist/release-notes.md"], check=True)
    release = api("/releases/tags/" + tag)
    actual = {asset["name"]: asset for asset in release["assets"]}
    expected = {Path(path).name: Path(path) for path in files}
    assert set(actual) == set(expected), "Release asset set does not match the verified build"
    import hashlib
    for name, path in expected.items():
        assert actual[name]["size"] == path.stat().st_size, "Uploaded size mismatch: " + name
        digest = actual[name].get("digest")
        if digest:
            assert digest == "sha256:" + hashlib.sha256(path.read_bytes()).hexdigest(), "Uploaded digest mismatch: " + name
    subprocess.run(["gh", "release", "edit", tag, "--draft=false", "--latest"], check=True)
    print("Published", api("/releases/tags/" + tag)["html_url"])


if __name__ == "__main__":
    main()
