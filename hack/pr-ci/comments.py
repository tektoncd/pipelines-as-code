"""Manages GitHub PR comments for linting feedback."""

from __future__ import annotations

import requests
from github import GitHubClient

PR_TITLE_COMMENT_MARKER = "<!-- pr-title-lint -->"


class CommentManager:
    """Manages GitHub PR comments for linting feedback."""

    def __init__(self, github: GitHubClient):
        self.github = github
        self.marker = PR_TITLE_COMMENT_MARKER

    def find_lint_comment(self) -> dict | None:
        """Find existing lint comment on the PR."""
        url = self.github._build_url(f"issues/{self.github.config.pr_number}/comments")
        try:
            comments = self.github.get_paginated(url)
            for comment in comments:
                body = comment.get("body", "") or ""
                if self.marker in body:
                    return comment
        except (requests.exceptions.RequestException, ValueError) as exc:
            print(f"Error fetching existing comments: {exc}")
        return None

    def delete_comment(self, comment_id: int | None) -> None:
        """Delete a comment by ID."""
        if comment_id is None:
            return

        url = f"{self.github.base_url}/repos/{self.github.config.repo_owner}/{self.github.config.repo_name}/issues/comments/{comment_id}"
        try:
            response = requests.delete(
                url, headers=self.github._headers(), timeout=self.github.timeout
            )
            if response.status_code not in (200, 204):
                print(
                    "Warning: Failed to delete previous lint comment "
                    f"(status {response.status_code})"
                )
        except requests.exceptions.RequestException as exc:
            print(f"Error deleting lint comment: {exc}")

    def upsert_comment(self, body: str, existing_comment: dict | None = None) -> None:
        """Create or update lint comment."""
        if existing_comment is None:
            existing_comment = self.find_lint_comment()

        if existing_comment:
            comment_id = existing_comment.get("id")
            if comment_id is None:
                print("Unexpected: comment id missing, posting new comment instead")
            else:
                url = f"{self.github.base_url}/repos/{self.github.config.repo_owner}/{self.github.config.repo_name}/issues/comments/{comment_id}"
                try:
                    response = requests.patch(
                        url,
                        headers=self.github._headers(),
                        json={"body": body},
                        timeout=self.github.timeout,
                    )
                    response.raise_for_status()
                    print("Updated existing lint comment")
                    return
                except requests.exceptions.RequestException as exc:
                    print(f"Error updating lint comment: {exc}")

        # Create a new comment
        url = self.github._build_url(f"issues/{self.github.config.pr_number}/comments")
        try:
            response = requests.post(
                url,
                headers=self.github._headers(),
                json={"body": body},
                timeout=self.github.timeout,
            )
            response.raise_for_status()
            print("Posted lint feedback as PR comment")
        except requests.exceptions.RequestException as exc:
            print(f"Error posting lint comment: {exc}")
