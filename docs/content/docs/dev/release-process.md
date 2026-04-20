# Pipelines-as-Code Release Process

## Overview

This document describes the release process for Pipelines-as-Code following the migration to the tektoncd organization. We use an automated draft-release workflow that bypasses manual tag pushes, which conflict with branch protection rules.

## Prerequisites

- Write access to the tektoncd/pipelines-as-code repository
- Access to GitHub Actions (to trigger workflow_dispatch)
- Container registry credentials (configured in repository secrets)
- GITHUB_TOKEN with `repo` scope (for draft release creation)

## Release Types

### 1. Stable Release

Stable releases follow semantic versioning: `v0.65.0`, `v0.66.0`, etc.

**When**: Before each major feature or quarterly cycle

**Process**:
```
1. Trigger release via GitHub Actions
2. Pipeline builds and tests
3. Draft release created with auto-generated notes
4. Manual review and publish
5. Git tag created automatically
```

### 2. Release Candidate

Pre-release versions: `v0.65.0-rc1`, `v0.65.0-rc2`

**When**: Before stable release for community testing

**Process**:
```
1. Same as stable release
2. Draft release marked as pre-release
3. Not included in "Latest Release" until final version published
```

### 3. Nightly Release

Automatic nightly builds: `v20250419-abc1234`

**When**: Every night at midnight UTC

**Process**:
```
1. Automated via scheduled pipeline (no manual trigger)
2. Draft pre-release created
3. NOT published (remains in draft)
4. Contains latest development code
```

---

## Step-by-Step Release Instructions

### Step 1: Trigger Release Pipeline

Navigate to: **[GitHub Actions → Release Pipelines-as-Code](https://github.com/tektoncd/pipelines-as-code/actions/workflows/release.yml)**

Click **"Run workflow"** button

Fill in the form:
- **Release version**: Required (e.g., `v0.65.0`)
- **Release name**: Optional (e.g., `Great Egret`)
  - Leave empty for auto-generated name
- **Previous release tag**: Optional (e.g., `v0.64.0`)
  - Leave empty for auto-detection
- **Tag this as latest**: Optional checkbox
  - Check for stable releases
  - Uncheck for RC/pre-release

Example:
```
Release version tag (e.g., v0.65.0): v0.65.0
Release name (e.g., "Great Egret"): Great Egret
Previous release tag (e.g., v0.64.0): [leave empty]
Tag this as latest release: ✓ checked
```

Click **"Run workflow"**

### Step 2: Monitor Pipeline

The Tekton release pipeline starts automatically. You can monitor progress in two places:

**Option A: Tekton Dashboard**
- Open: https://tekton.pipelinesascode.com
- Filter by: `pipelines-as-code-release-*` pipelineRuns
- Watch: Build → Test → Publish → Signing → Release

**Option B: GitHub Actions**
- Open: https://github.com/tektoncd/pipelines-as-code/actions/workflows/release.yml
- Click the latest run to see status

**Expected Duration**: 20-30 minutes

The pipeline will:
1. Clone the repository at the specified revision
2. Run pre-release checks (version validation, etc.)
3. Execute unit tests and integration tests
4. Build binaries for all platforms
5. Publish container images to GHCR
6. Upload release artifacts to Google Cloud Storage
7. Wait for Tekton Chains to sign container images
8. Generate release notes from merged PRs
9. Create GitHub draft release with attachments

### Step 3: Review Draft Release

Once the pipeline completes, a **draft release** is created on GitHub.

Navigate to: **[Releases](https://github.com/tektoncd/pipelines-as-code/releases)**

You should see a new draft (marked with "Draft" badge):
```
✏️  Draft  v0.65.0 - Great Egret
```

Click the release to open it.

**Review**:
- [ ] Release notes are complete and accurate
- [ ] No placeholder text like "TODO" or "Fill in..."
- [ ] PR credits are listed
- [ ] Container image SHA256 digests are correct
- [ ] Release artifacts are attached
  - `release.yaml`
  - `release-nightly.yaml`
  - `release-legacy.yaml`
  - etc.

**Examples of auto-generated sections**:

```markdown
# Features

* ✨ Support multi-namespace pipeline triggers (#2677) (@singha200)
* ✨ Add project-level HTTP access tokens for Bitbucket DC (#2685) (@Ru13en)

### Fixes

* 🐛 PAC shouldn't request approval when no pipelines exist (#2692) (@chmouel)

### Docs

* 📚 Update profiling guide after OpenTelemetry migration (#2633) (@theakshaypant)

## Thanks

Thanks to these contributors:
* ❤️  @singha200
* ❤️  @Ru13en
* ❤️  @chmouel
* ❤️  @theakshaypant
```

### Step 4: Edit Release Notes (if needed)

If release notes need adjustments:

1. Click "Edit" button on the draft release
2. Update the markdown in the "Write" tab
3. Save changes by clicking "Update release"

Common edits:
- Add upgrade notices: "⚠️ Breaking change: feature X now requires Y"
- Add deprecation notices: "🚫 Deprecated: feature X will be removed in v1.0"
- Reorder sections for clarity
- Add examples or links

### Step 5: Publish Release

When satisfied with the release:

1. Click **"Publish release"** button
2. GitHub will:
   - Create git tag (v0.65.0)
   - Mark release as published
   - Send notifications to watchers
   - Update repository to show release

**Important**: Publishing creates the git tag automatically. This bypasses branch protection rules.

### Step 6: Verify Release (Post-Publish)

After publishing, verify:

- [ ] Git tag created: `git fetch origin && git show v0.65.0`
- [ ] Release visible as "Latest": Check [Releases page](https://github.com/tektoncd/pipelines-as-code/releases)
- [ ] Container images available:
  ```bash
  skopeo inspect docker://ghcr.io/tektoncd/pipelines-as-code:v0.65.0
  ```
- [ ] Release artifacts accessible:
  ```bash
  curl -L https://infra.tekton.dev/tekton-releases/pipelines-as-code/previous/v0.65.0/release.yaml
  ```
- [ ] Attestation in Rekor (check release notes for UUID)

---

## Release Checklist

### Pre-Release (Before Triggering Pipeline)

- [ ] Code changes merged and tested in main branch
- [ ] Version number decided (X.Y.Z semantic versioning)
- [ ] Release notes/changelog prepared (or ready for auto-generation)
- [ ] Container registry secrets updated if needed
- [ ] Release name chosen (or accepting auto-generated)
- [ ] GITHUB_TOKEN secret still valid
- [ ] Team notified of upcoming release

### During Pipeline (20-30 minutes)

- [ ] Monitor pipeline progress
- [ ] No pipeline failures occur
- [ ] All tasks complete successfully
- [ ] Draft release appears on GitHub
- [ ] Container images pushed to GHCR

### Post-Pipeline (Before Publishing)

- [ ] Review draft release carefully
- [ ] Verify release notes completeness
- [ ] Check artifact attachments
- [ ] Test release artifacts locally (if critical)
- [ ] Get team sign-off if needed

### Publishing

- [ ] Click "Publish release"
- [ ] Confirm git tag created
- [ ] Verify release visible as "Latest"

### Post-Release (After Publishing)

- [ ] Announce release on Slack/mailing list
- [ ] Update release notes on website
- [ ] Link to release in blog post (if applicable)
- [ ] Monitor for issues in new version
- [ ] Archive release notes for records

---

## Troubleshooting

### Draft Release Not Created

**Symptom**: Pipeline completes but no GitHub draft release appears

**Possible Causes**:
1. `github-secret` workspace not bound to pipeline
2. GITHUB_TOKEN expired or missing required scopes
3. Tekton Chains signing failed (check `wait-for-chains` task logs)

**Solution**:
```bash
# Check if github-secret exists
kubectl get secret -n tekton-ci github-token

# Verify token has required scopes
# Token must have: repo, write:packages, admin:repo_hook

# Check task logs
kubectl logs $(kubectl get pod -n tekton-ci -l tekton.dev/pipelineTask=create-draft-release -o jsonpath='{.items[0].metadata.name}') -n tekton-ci
```

### Git Tag Not Created After Publishing

**Symptom**: Release published but `git show v0.65.0` fails

**Possible Causes**:
1. Publishing didn't complete (still in draft state)
2. GitHub API delay (up to 1 minute)
3. Branch protection preventing tag creation

**Solution**:
1. Verify release status on GitHub (should say "Latest" not "Draft")
2. Wait up to 1 minute and retry
3. If issue persists, create tag manually:
   ```bash
   git tag v0.65.0 $(git rev-parse HEAD)
   git push origin v0.65.0
   ```

### Container Images Not Signed

**Symptom**: Release notes mention "Rekor UUID: unknown" or "Signing status: failed"

**Possible Causes**:
1. Tekton Chains controller not running
2. Image signing timeout (wait-for-chains max 30 minutes)
3. Chains attestation not uploaded to Rekor

**Solution**:
1. Check Chains controller: `kubectl get pods -n tekton-chains`
2. Restart Chains if stuck: `kubectl rollout restart deployment tekton-chains-controller -n tekton-chains`
3. For next release, re-run if timeout occurred

### Release Artifacts Missing

**Symptom**: Release page shows no attachments (release.yaml, etc.)

**Possible Causes**:
1. `publish-to-bucket` task failed
2. Artifact files not found at build time
3. GCS credentials invalid

**Solution**:
1. Check `publish-to-bucket` task logs
2. Verify build artifacts were created: `kubectl logs -n tekton-ci <pod> -c build`
3. Verify GCS credentials secret exists

### Manual Release Process (Fallback)

If the automated pipeline fails completely:

1. **Checkout version**: `git checkout vX.Y.Z` or commit SHA
2. **Build manually**:
   ```bash
   make install
   ko build ./cmd/pac
   ```
3. **Create release manually**:
   ```bash
   gh release create v0.65.0 \
     --draft \
     --prerelease \
     --title "Release v0.65.0" \
     --notes "See changelog at..."
   ```
4. **Upload artifacts**:
   ```bash
   gh release upload v0.65.0 release.yaml release-legacy.yaml ...
   ```
5. **Publish**:
   ```bash
   gh release edit v0.65.0 --draft=false
   ```

---

## Version Management

### Semantic Versioning

Format: `vMAJOR.MINOR.PATCH[-PRERELEASE]`

Examples:
- `v0.65.0` - Stable release
- `v0.65.0-rc1` - Release candidate
- `v0.65.0-rc2` - Second release candidate
- `v0.66.0-alpha` - Alpha preview

**Rules**:
- MAJOR: Breaking changes or significant feature releases
- MINOR: New features, non-breaking changes
- PATCH: Bug fixes and minor improvements
- PRERELEASE: `-rc1`, `-rc2`, `-alpha`, `-beta` (optional)

### Auto-Detection of Previous Release

If you leave the "Previous release tag" field empty:

1. Pipeline runs `git tag --sort=-v:refname` to list all tags
2. Filters for tags matching `v*` pattern
3. Selects most recent tag smaller than current version
4. Uses it for changelog generation

This works for most cases but you can override by providing a value.

---

## Release Candidate Process

For stability on major releases, consider releasing RCs first:

### RC Phase (1-2 weeks before stable)

1. **Release v0.65.0-rc1**: Full release pipeline, marked as pre-release
   - Community testing period starts
   - Bug reports addressed in main branch
   
2. **Release v0.65.0-rc2** (if needed): Additional fixes
   - For critical bugs found in RC1
   
3. **Release v0.65.0**: Final stable release
   - Inherits RC testing + final validation

**Benefits**:
- Community finds bugs early
- Time for fixes before stable
- Higher confidence in final release

---

## Nightly Release Notes

Nightly builds are created automatically every 24 hours:

- **Trigger**: Scheduled GitHub Actions (midnight UTC)
- **Version**: `v20250419-abc1234` (date + commit prefix)
- **Status**: Draft + Pre-release (not published)
- **Retention**: Kept for 30 days, then deleted

**Purpose**: Allow early testing of latest features

**How to Use**:
```bash
# Install nightly build
kubectl apply -f https://infra.tekton.dev/tekton-releases/pipelines-as-code/nightly/release.yaml
```

---

## FAQs

**Q: Why draft releases instead of pre-releases?**
A: Draft releases don't show up as "releases" to end users. Pre-releases do. Draft + manual publish gives more control.

**Q: What if I make a mistake while editing release notes?**
A: Edit the release again (before publishing) or delete the draft and re-run the pipeline.

**Q: Can I release from a branch instead of main?**
A: Yes, specify the branch name or commit SHA in the "Git revision" field of the pipeline.

**Q: How do I release a patch version after a stable release?**
A: Create a release branch (e.g., `release-v0.65`), cherry-pick fixes, then release from that branch.

**Q: What if the pipeline times out?**
A: Timeouts can happen during image signing (30-minute limit). Re-run the pipeline - it will pick up where it left off.

**Q: Do I need to update the version in Go files?**
A: Version is typically read from git tags (`git tag --points-at HEAD`). No need to update in code.

---

## Contacts & Resources

- **Release Questions**: Reach out on Slack #pipelines-as-code
- **Pipeline Issues**: File issues on [GitHub](https://github.com/tektoncd/pipelines-as-code/issues)
- **Tekton Release Docs**: https://github.com/tektoncd/pipeline/tree/main/tekton
- **GoReleaser Docs**: https://goreleaser.com/

---

## See Also

- [.github/workflows/release.yml](.github/workflows/release.yml) - GitHub Actions workflow
- [.tekton/release-pipeline.yaml](.tekton/release-pipeline.yaml) - Tekton pipeline
- [.goreleaser.yml](.goreleaser.yml) - Release configuration
- [CONTRIBUTING.md](CONTRIBUTING.md) - Contributing guide
