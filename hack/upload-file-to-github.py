#!/usr/bin/env python3
# Upload a file to github directly to a branch and create tags and release branch
# Example:
#
# ./hack/upload-file-to-github.py -t token \
# --owner-repository owner/repo
# --from-tag refs/heads/0.5.5 \
# -f file.txt:dest.txt \
# -f hello.txt:moto.txt
# -m "Add file"
# --update-tags=stable,0.5
#
# This will :
#
# - On Github owner/repo using the refs/heads/0.5.5 tag (can be a branch/sha or whatever git ref) using token token
# - Create a branch from 0.5.5 tag
# - Create the branch release-0.5.5
# - Upload the file file.txt to the destination dest.txt
# - Upload another file hello.txt to the destination moto.txt
# - Force update or create the tag stable to point to the release-0.5.5 branch
# - Force update or create the tag 0.5 to point to the release-0.5.5 branch
import argparse
import base64
import http.client
import json
import os.path
import re
import time
import urllib

RE_RELEASE = re.compile(r"(\d+\.\d+)\.\d+")
GIT_NAME = "Openshift Pipeline Release Team"
GIT_EMAIL = "pipelines@redhat.com"


class UploadFileToGithubException(Exception):
    pass


def github_request(
    token: str,
    method: str,
    url: str,
    headers=None,
    data=None,
    params=None,
    return_status_on_error: bool = False,
):
    if not headers:
        headers = {}

    headers.setdefault("Authorization", "Bearer " + token)
    headers.setdefault("User-Agent", "Tekton asa Code")
    headers.setdefault("Accept", "application/vnd.github.v3+json")
    if not url.startswith("https://"):
        url = "https://api.github.com" + url

    url_parsed = urllib.parse.urlparse(url)
    url_path = url_parsed.path
    if params:
        url_path += "?" + urllib.parse.urlencode(params)

    data = data and json.dumps(data)
    conn = http.client.HTTPSConnection(url_parsed.hostname)
    conn.request(method, url_path, body=data, headers=headers)
    response = conn.getresponse()
    if response.status == 302:
        return github_request(token, method, response.headers["Location"])

    if response.status >= 400:
        headers.pop("Authorization", None)
        if return_status_on_error:
            return (response, {})

        raise UploadFileToGithubException(
            f"Error: {response.status} - {json.loads(response.read())} - {method} - {url} - {data} - {headers}"
        )

    return (response, json.loads(response.read()))


def make_or_update_ref(token, owner_repository, ref, sha: str) -> None:
    resp, jeez = github_request(
        token,
        "GET",
        f"/repos/{owner_repository}/git/{ref}",
        return_status_on_error=True,
    )

    if resp.status == 404 or jeez["ref"] != ref:
        resp, _ = github_request(
            token,
            "POST",
            f"/repos/{owner_repository}/git/refs",
            data={"ref": ref, "sha": sha},
        )
        print(f"{ref} has been created to {sha}")
    else:
        _, _ = github_request(
            token,
            "PATCH",
            f"/repos/{owner_repository}/git/{ref}",
            data={"ref": ref, "sha": sha, "force": True},
        )
        print(f"{ref} has been updated to {sha}")


def create_from_refs(args):
    _, jeez = github_request(
        args.token, "GET", f"/repos/{args.owner_repository}/git/{args.from_ref}"
    )
    last_commit_sha = jeez["object"]["sha"]
    if jeez["object"]["type"] == "tag":
        _, jeez = github_request(args.token, "GET", jeez["object"]["url"])
        last_commit_sha = jeez["object"]["sha"]

    print("TAG SHA: " + last_commit_sha)
    print(f"Create or update branch: {os.path.basename(args.to_ref)}")
    make_or_update_ref(args.token, args.owner_repository, args.to_ref, last_commit_sha)
    return upload_to_github(args)


def upload_to_github(args):
    last_commit_sha = None
    if not args.to_ref:
        raise UploadFileToGithubException("Need a to-ref args")
    if not args.filename:
        raise UploadFileToGithubException("Need at least one filename")

    # Get last commit SHA of a branch
    _, jeez = github_request(
        args.token, "GET", f"/repos/{args.owner_repository}/git/{args.to_ref}"
    )
    last_commit_sha = jeez["object"]["sha"]
    for entry in args.filename:
        left, dest = entry.split(":")

        content: str = ""
        if os.path.exists(left):
            with open(left, "r", encoding="utf-8") as f:
                content: str = f.read()
            print(
                f"Uploading file {left} to destination {dest} based on {last_commit_sha}"
            )
        else:
            content: str = left.strip()
            print(
                f"Setting value {left} into the destionation {dest} based on {last_commit_sha}"
            )

        base64content = base64.b64encode(content.encode())
        _, jeez = github_request(
            args.token,
            "POST",
            f"/repos/{args.owner_repository}/git/blobs",
            data={"content": base64content.decode(), "encoding": "base64"},
        )
        blob_content_sha = jeez["sha"]

        _, jeez = github_request(
            args.token,
            "POST",
            f"/repos/{args.owner_repository}/git/trees",
            data={
                "base_tree": last_commit_sha,
                "tree": [
                    {
                        "path": dest,
                        "mode": "100644",
                        "type": "blob",
                        "sha": blob_content_sha,
                    }
                ],
            },
        )
        tree_sha = jeez["sha"]

        _, jeez = github_request(
            args.token,
            "POST",
            f"/repos/{args.owner_repository}/git/commits",
            data={
                "message": args.message,
                "author": {
                    "name": GIT_NAME,
                    "email": GIT_EMAIL,
                },
                "parents": [last_commit_sha],
                "tree": tree_sha,
            },
        )
        last_commit_sha = jeez["sha"]
        print(f"Last commit SHA: {last_commit_sha}")
        time.sleep(30)

        _, jeez = github_request(
            args.token,
            "PATCH",
            f"/repos/{args.owner_repository}/git/{args.to_ref}",
            data={"sha": last_commit_sha},
        )
    return last_commit_sha


def parse_args():
    parser = argparse.ArgumentParser(description="Upload a file to github ref")
    parser.add_argument("--filename", "-f", required=True, action="append")
    parser.add_argument("--message", "-m", required=True)
    parser.add_argument("--owner-repository", "-o", required=True)
    parser.add_argument("--token", "-t", required=True)
    parser.add_argument("--to-ref", "-r", required=False)
    parser.add_argument("--from-ref", required=False)
    return parser.parse_args()


def main(args):
    if args.from_ref:
        create_from_refs(args)
    else:
        upload_to_github(args)


if __name__ == "__main__":
    main(parse_args())
