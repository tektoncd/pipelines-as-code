#!/usr/bin/env bash
# description: Check documentation links for 404s using htmltest,
# and verify that internal links use Hugo's relref shortcode.
#
# Usage:
#   ./hack/check-links.sh          # Check only (exits 1 if bare relative links found)
#   ./hack/check-links.sh --fix    # Auto-fix bare relative links to use relref, then check
#
# This script first checks that markdown files don't use bare relative
# links (which break in Hugo's nested index.html output), then builds
# the Hugo docs site and runs htmltest against the generated HTML to
# detect broken internal and external links.
set -eufo pipefail

FIX_MODE=false
if [[ "${1:-}" == "--fix" ]]; then
  FIX_MODE=true
fi

TOPDIR=$(git rev-parse --show-toplevel)
TMPDIR=${TOPDIR}/tmp
HUGO_VERSION=${HUGO_VERSION:-0.166.0}
HTMLTEST_VERSION=${HTMLTEST_VERSION:-0.17.0}
HUGO_BIN=${TMPDIR}/hugo/hugo
HTMLTEST_BIN=${TMPDIR}/htmltest/htmltest
DOCS_BUILD_DIR=${TMPDIR}/docs-build-test
DOCS_CONTENT_DIR=${TOPDIR}/docs/content
CHECK_EXTERNAL=${CHECK_EXTERNAL:-true}

# --- Step 1: Check (and optionally fix) that internal links use relref shortcode ---
# Bare relative links like [text](some-page) break when Hugo generates
# nested index.html files. Authors should use {{< relref "/docs/path" >}}.
# Excluded from checking:
#   - External URLs (http://, https://)
#   - Anchor-only links (#section)
#   - Links already using relref or other Hugo shortcodes
#   - Image references ![alt](path)
#   - mailto: and tel: links
#   - Absolute paths starting with / (valid Hugo site-root paths)
#   - Lines containing Hugo shortcodes (card, relref, etc.)
#   - Lines with raw HTML (href=)
if [[ "$FIX_MODE" == "true" ]]; then
  echo "==> Fixing bare relative links to use relref shortcode..."
else
  echo "==> Checking documentation links use relref shortcode..."
fi
relref_count=0

while IFS= read -r -d '' file; do
  # Compute the directory of this file relative to docs/content/
  # e.g., docs/content/docs/operations/settings.md -> docs/operations
  file_rel="${file#"${DOCS_CONTENT_DIR}/"}"
  file_dir=$(dirname "$file_rel")
  # Normalize: "." means root of content
  if [[ "$file_dir" == "." ]]; then
    file_dir=""
  fi

  if [[ "$FIX_MODE" == "true" ]]; then
    # Use perl to find and replace bare relative links in-place
    # Pass the file's directory so we can compute absolute relref paths
    FILE_DIR="$file_dir" perl -pi -e '
			BEGIN { $dir = $ENV{FILE_DIR}; }
			# Skip lines with Hugo shortcodes
			next if /\{\{[<%]/;
			# Skip lines with raw HTML href
			next if /href=/;
			# Replace [text](bare-target) but NOT ![text](target)
			s{(?<!!)\[([^\]]*)\]\(([^)]+)\)}{
				my ($text, $target) = ($1, $2);
				# Only fix bare relative links
				if ($target =~ m{^https?://} ||
					$target =~ m{^#} ||
					$target =~ m{^(mailto|tel):} ||
					$target =~ m/\{\{/ ||
					$target =~ m{^/}) {
					"[$text]($target)";
				} else {
					# Separate anchor from path
					my ($path, $anchor) = $target =~ m{^([^#]*)(.*)$};
					# Resolve relative path against file directory
					my $abs;
					if ($dir eq "") {
						$abs = $path;
					} else {
						$abs = "$dir/$path";
					}
					# Normalize: collapse foo/../bar -> bar
					while ($abs =~ s{[^/]+/\.\./}{}) {}
					$abs =~ s{/\.\./}{/}g;
					# Remove trailing /
					$abs =~ s{/$}{};
					# Remove .md suffix
					$abs =~ s{\.md$}{};
					# Prepend /
					$abs = "/$abs" unless $abs =~ m{^/};
					"[$text]({{< relref \"${abs}${anchor}\" >}})";
				}
			}ge;
		' "$file"
  fi

  # Now scan the (possibly modified) file for remaining bare relative links
  line_num=0
  while IFS= read -r line; do
    line_num=$((line_num + 1))

    # Skip lines containing Hugo shortcodes (card, relref, etc.)
    if [[ "$line" =~ \{\{[\<\%] ]]; then
      continue
    fi

    # Skip lines with raw HTML tags
    if [[ "$line" =~ href= ]]; then
      continue
    fi

    # Skip lines with no markdown link syntax
    if ! [[ "$line" =~ \]\( ]]; then
      continue
    fi

    # Extract bare relative link targets using perl
    matches=$(echo "$line" | perl -ne '
			while (/(?<!!)\[([^\]]*)\]\(([^)]+)\)/g) {
				my $target = $2;
				next if $target =~ m{^https?://};
				next if $target =~ m{^#};
				next if $target =~ m{^(mailto|tel):};
				next if $target =~ m{\{\{};
				next if $target =~ m{^/};
				print "$target\n";
			}
		' 2>/dev/null || true)

    if [[ -n "$matches" ]]; then
      while IFS= read -r target; do
        if [[ "$FIX_MODE" == "true" ]]; then
          echo "  WARNING: ${file}:${line_num}: could not auto-fix bare relative link '${target}'"
        else
          echo "  ERROR: ${file}:${line_num}: bare relative link '${target}' should use {{< relref >}}"
        fi
        relref_count=$((relref_count + 1))
      done <<<"$matches"
    fi
  done <"$file"
done < <(find "$DOCS_CONTENT_DIR" -name '*.md' -print0)

if [[ "$FIX_MODE" == "true" ]]; then
  if [[ $relref_count -gt 0 ]]; then
    echo ""
    echo "WARNING: ${relref_count} link(s) could not be auto-fixed. Please fix manually."
  else
    # Show what changed
    changed=$(git -C "${TOPDIR}" diff --name-only -- docs/content/ 2>/dev/null || true)
    if [[ -n "$changed" ]]; then
      echo "  Fixed files:"
      echo "$changed" | while IFS= read -r f; do echo "    $f"; done
      echo ""
      echo "  Run 'make check-links' to verify the fixes."
    else
      echo "  No bare relative links found, nothing to fix."
    fi
  fi
else
  if [[ $relref_count -gt 0 ]]; then
    echo ""
    echo "Found ${relref_count} bare relative link(s). Use {{< relref \"/docs/path\" >}} instead."
    echo "Run 'make fix-links' to auto-fix them."
    exit 1
  fi
  echo "  All internal documentation links use relref correctly."
fi

# --- Step 2: Build Hugo site and run htmltest ---
# Download Hugo if not present
echo "==> Ensuring Hugo ${HUGO_VERSION} is available..."
"${TOPDIR}/hack/download-hugo.sh" "${HUGO_VERSION}" "${TMPDIR}/hugo"

# Download htmltest if not present
echo "==> Ensuring htmltest ${HTMLTEST_VERSION} is available..."
"${TOPDIR}/hack/download-htmltest.sh" "${HTMLTEST_VERSION}" "${TMPDIR}/htmltest"

# Clean stale output directories that cause Hugo to deadlock
rm -rf "${DOCS_BUILD_DIR}" "${TOPDIR}/docs/public"

# Build Hugo site
echo "==> Building Hugo documentation site..."
"${HUGO_BIN}" build --gc --minify -s "${TOPDIR}/docs/" -d "${DOCS_BUILD_DIR}"

# Verify Markdown output routes and canonical links.
"${TOPDIR}/hack/check-markdown-output.sh" "${DOCS_BUILD_DIR}"

# Run htmltest
echo "==> Running htmltest link checker..."
HTMLTEST_CONF="${TOPDIR}/docs/.htmltest.yml"
if [[ "${CHECK_EXTERNAL}" == "false" || "${CHECK_EXTERNAL}" == "0" ]]; then
  echo "==> External link checking disabled (CHECK_EXTERNAL=${CHECK_EXTERNAL})"
  HTMLTEST_CONF="${TMPDIR}/.htmltest-internal.yml"
  sed 's/^CheckExternal:.*/CheckExternal: false/' "${TOPDIR}/docs/.htmltest.yml" > "${HTMLTEST_CONF}"
fi
"${HTMLTEST_BIN}" -c "${HTMLTEST_CONF}"
