#!/usr/bin/env -S uv --quiet run --script
# /// script
# requires-python = ">=3.12"
# dependencies = [
#     "requests",
# ]
# ///
"""Clean up GitLab projects older than a given number of days in a group.

Uses TEST_GITLAB_API_URL, TEST_GITLAB_TOKEN, and TEST_GITLAB_GROUP environment
variables by default (same as the E2E test suite), but all values can be
overridden via CLI flags.

Examples:
    # Dry-run (default) — show what would be deleted:
    ./hack/cleanup-gitlab-projects.py

    # Actually delete:
    ./hack/cleanup-gitlab-projects.py --force

    # Custom age threshold:
    ./hack/cleanup-gitlab-projects.py --days 3 --force
"""

import argparse
import os
import sys
from datetime import datetime, timezone

import requests


def get_projects(base_url: str, token: str, group: str) -> list[dict]:
    """Return all projects in the given group, handling pagination."""
    headers = {"PRIVATE-TOKEN": token}
    url = f"{base_url}/api/v4/groups/{requests.utils.quote(group, safe='')}/projects"
    params: dict = {"per_page": 100, "page": 1, "include_subgroups": False}
    projects: list[dict] = []
    while True:
        resp = requests.get(url, headers=headers, params=params, timeout=30)
        resp.raise_for_status()
        batch = resp.json()
        if not batch:
            break
        projects.extend(batch)
        params["page"] += 1
    return projects


def delete_project(base_url: str, token: str, project_id: int) -> None:
    headers = {"PRIVATE-TOKEN": token}
    url = f"{base_url}/api/v4/projects/{project_id}"
    resp = requests.delete(url, headers=headers, timeout=30)
    resp.raise_for_status()


def main() -> None:
    parser = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    parser.add_argument(
        "--api-url",
        default=os.getenv("TEST_GITLAB_API_URL", "https://gitlab.pipelinesascode.com"),
        help="GitLab API base URL (default: $TEST_GITLAB_API_URL)",
    )
    parser.add_argument(
        "--token",
        default=os.getenv("TEST_GITLAB_TOKEN", ""),
        help="GitLab private token (default: $TEST_GITLAB_TOKEN)",
    )
    parser.add_argument(
        "--group",
        default=os.getenv("TEST_GITLAB_GROUP", "pac-e2e-tests"),
        help="GitLab group path (default: $TEST_GITLAB_GROUP)",
    )
    parser.add_argument(
        "--days",
        type=int,
        default=7,
        help="Delete projects older than this many days (default: 7)",
    )
    parser.add_argument(
        "--force",
        action="store_true",
        help="Actually delete projects (default is dry-run)",
    )
    args = parser.parse_args()

    if not args.token:
        print(
            "ERROR: GitLab token is required. Set TEST_GITLAB_TOKEN or pass --token.",
            file=sys.stderr,
        )
        sys.exit(1)

    base_url = args.api_url.rstrip("/")
    now = datetime.now(tz=timezone.UTC)

    print(f"Listing projects in group '{args.group}' on {base_url} ...")
    projects = get_projects(base_url, args.token, args.group)
    print(f"Found {len(projects)} project(s).")

    to_delete = []
    for proj in projects:
        created = datetime.fromisoformat(proj["created_at"])
        age = now - created
        if age.days >= args.days:
            to_delete.append((proj, age))

    if not to_delete:
        print(f"No projects older than {args.days} day(s). Nothing to do.")
        return

    print(f"\n{len(to_delete)} project(s) older than {args.days} day(s):\n")
    for proj, age in to_delete:
        print(
            f"  {proj['path_with_namespace']} (ID {proj['id']}, created {proj['created_at']}, {age.days}d old)"
        )

    if not args.force:
        print("\nDry-run mode. Pass --force to delete these projects.")
        return

    print()
    errors = 0
    for proj, age in to_delete:
        name = proj["path_with_namespace"]
        try:
            delete_project(base_url, args.token, proj["id"])
            print(f"  Deleted {name} (ID {proj['id']})")
        except requests.HTTPError as exc:
            print(f"  ERROR deleting {name}: {exc}", file=sys.stderr)
            errors += 1

    deleted = len(to_delete) - errors
    print(f"\nDone. Deleted {deleted} project(s).", end="")
    if errors:
        print(f" {errors} error(s).", end="")
    print()


if __name__ == "__main__":
    main()
