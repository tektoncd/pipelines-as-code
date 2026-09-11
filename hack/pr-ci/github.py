"""Client for interacting with GitHub API."""

from __future__ import annotations

from collections.abc import Sequence

import requests

from config import Config

GITHUB_API_BASE = "https://api.github.com"


class GitHubClient:
    """Client for interacting with GitHub API."""

    def __init__(self, config: Config):
        self.config = config
        self.base_url = GITHUB_API_BASE
        self.timeout = 300

    def _headers(self) -> dict[str, str]:
        """Get headers for GitHub API requests."""
        return {
            "Authorization": f"token {self.config.github_token}",
            "Accept": "application/vnd.github.v3+json",
        }

    def _build_url(self, endpoint: str) -> str:
        """Build full API URL for repository endpoint."""
        return (
            f"{self.base_url}/repos/{self.config.repo_owner}"
            f"/{self.config.repo_name}/{endpoint}"
        )

    def get_paginated(self, url: str) -> list[dict]:
        """Fetch all pages from a GitHub API endpoint."""
        all_data: list[dict] = []
        headers = self._headers()
        current_url: str | None = url

        while current_url:
            response = requests.get(current_url, headers=headers, timeout=self.timeout)
            response.raise_for_status()
            data = response.json()
            if not isinstance(data, list):
                raise TypeError(
                    "Expected list response from GitHub API, got"
                    f" {type(data).__name__} instead"
                )
            all_data.extend(data)

            # Get next page URL from Link header
            link_header = response.headers.get("Link", "")
            current_url = None
            for link in link_header.split(","):
                if 'rel="next"' in link:
                    current_url = link.split(";")[0].strip("<>")
                    break

        return all_data

    def get_pr_info(self) -> dict | None:
        """Get pull request information."""
        url = self._build_url(f"pulls/{self.config.pr_number}")
        headers = self._headers()

        try:
            response = requests.get(url, headers=headers, timeout=self.timeout)
            response.raise_for_status()
            return response.json()
        except requests.exceptions.RequestException as exc:
            print(f"Error fetching PR info: {exc}")
            return None

    def get_pr_files(self) -> list[str]:
        """Get list of files changed in the PR."""
        url = self._build_url(f"pulls/{self.config.pr_number}/files")
        try:
            files_data = self.get_paginated(url)
            files_changed: list[str] = []
            for file_info in files_data:
                status = file_info.get("status", "modified")[0].upper()
                filename = file_info.get("filename", "")
                files_changed.append(f"{status}\t{filename}")
            return files_changed
        except (requests.exceptions.RequestException, ValueError) as exc:
            print(f"Error fetching PR files: {exc}")
            return []

    def get_pr_commits(self) -> list[dict]:
        """Get all commits in the PR."""
        url = self._build_url(f"pulls/{self.config.pr_number}/commits")
        try:
            return self.get_paginated(url)
        except (requests.exceptions.RequestException, ValueError) as exc:
            print(f"Error fetching PR commits: {exc}")
            return []

    def get_available_labels(self) -> list[dict]:
        """Get all available labels in the repository."""
        url = self._build_url("labels")
        try:
            labels_data = self.get_paginated(url)
            return [
                {
                    "name": label["name"],
                    "description": label.get("description", "") or "",
                }
                for label in labels_data
            ]
        except (requests.exceptions.RequestException, ValueError) as exc:
            print(f"Error fetching available labels: {exc}")
            return []

    def create_issue(
        self, title: str, body: str, labels: list[str] | None = None
    ) -> dict | None:
        """Create a new GitHub issue."""
        url = self._build_url("issues")
        headers = self._headers()

        issue_data = {"title": title, "body": body}

        if labels:
            issue_data["labels"] = labels

        try:
            response = requests.post(
                url, headers=headers, json=issue_data, timeout=self.timeout
            )
            response.raise_for_status()
            issue = response.json()
            print(f"Successfully created issue #{issue.get('number')}: {title}")
            return issue
        except requests.exceptions.RequestException as exc:
            print(f"Error creating issue: {exc}")
            if hasattr(exc, "response") and exc.response:
                print(f"Response: {exc.response.text}")
            return None

    def link_pr_to_issue(self, issue_number: int) -> bool:
        """Link this PR to a GitHub issue by updating PR body with closing keyword."""
        try:
            # Get current PR info
            pr_info = self.get_pr_info()
            if not pr_info:
                return False

            current_body = pr_info.get("body", "") or ""

            # Add closing keyword if not already present
            closing_text = f"\n\nCloses #{issue_number}"
            if f"#{issue_number}" not in current_body:
                new_body = current_body + closing_text

                # Update PR body
                url = self._build_url(f"pulls/{self.config.pr_number}")
                headers = self._headers()

                response = requests.patch(
                    url, headers=headers, json={"body": new_body}, timeout=self.timeout
                )
                response.raise_for_status()
                print(f"Successfully linked PR to issue #{issue_number}")
                return True
            else:
                print(f"PR already references issue #{issue_number}")
                return True

        except requests.exceptions.RequestException as exc:
            print(f"Error linking PR to issue: {exc}")
            return False

    def add_labels(self, labels: Sequence[str]) -> None:
        """Add labels to the PR."""
        if not labels:
            print("No labels to add")
            return

        url = self._build_url(f"issues/{self.config.pr_number}/labels")
        headers = self._headers()

        try:
            response = requests.post(
                url, headers=headers, json=labels, timeout=self.timeout
            )
            response.raise_for_status()
            print(f"Successfully added labels: {labels}")
        except requests.exceptions.RequestException as exc:
            print(f"Error adding labels: {exc}")
            if hasattr(exc, "response") and exc.response:
                print(f"Response: {exc.response.text}")
