"""Utility functions for PR linting and updates."""

from __future__ import annotations

import re


def detect_modified_providers(files_changed: list[str]) -> set[str]:
    """Detect which provider subdirectories were modified."""
    providers = set()
    provider_pattern = re.compile(r"pkg/provider/(github|gitlab|gitea|bitbucket\w*)/")

    for file in files_changed:
        file_path = file.split("\t")[1] if "\t" in file else file
        if match := provider_pattern.search(file_path):
            provider = match.group(1)
            # Normalize bitbucket variants to just "bitbucket"
            if provider.startswith("bitbucket"):
                providers.add("bitbucket")
            else:
                providers.add(provider)

    return providers


def check_file_categories(files_changed: list[str]) -> tuple[bool, bool, bool]:
    """Check which file categories are present in the changes.

    Returns:
        Tuple of (has_docs_files, has_test_files, has_provider_files)
    """
    has_docs = False
    has_test = False
    has_provider = False

    for file in files_changed:
        file_path = file.split("\t")[1] if "\t" in file else file
        if file_path.startswith("docs/"):
            has_docs = True
        if file_path.startswith("test/"):
            has_test = True
        if file_path.startswith("pkg/provider/"):
            has_provider = True

    return has_docs, has_test, has_provider
