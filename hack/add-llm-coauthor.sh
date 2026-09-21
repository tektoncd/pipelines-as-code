#!/usr/bin/env bash
# Author: Chmouel Boudjnah <chmouel@chmouel.com>
# Interactive tool to add AI assistant Co-authored-by lines to git commits

set -euo pipefail

# Function to display help information
show_help() {
  cat <<'EOF'
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
🤖 LLM Co-author Attribution Tool
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

DESCRIPTION:
  Interactive tool to add AI assistant Co-authored-by lines to git commits.
  Promotes transparency in AI-assisted development by properly crediting
  LLM contributions in commit messages following Git trailer conventions.

USAGE:
  ./hack/add-llm-coauthor                         # Interactive mode (select commits with fzf)
  ./hack/add-llm-coauthor HEAD HEAD^              # Direct mode (specify commits as arguments)
  ./hack/add-llm-coauthor -a cursor,claude HEAD  # Auto mode (specify LLMs and commits)
  ./hack/add-llm-coauthor -a gemini               # Auto mode with interactive commit selection
  ./hack/add-llm-coauthor -r cursor,claude HEAD  # Remove mode (remove specific LLMs)
  ./hack/add-llm-coauthor -r                      # Remove mode with interactive selection
  ./hack/add-llm-coauthor --help                  # Show this help message

REQUIREMENTS:
  - fzf (for interactive selection)
  - git repository with commits

WORKFLOW:
  1. 📝 Select commits interactively using fzf
  2. 🤖 Choose AI assistants from predefined list
  3. ✨ Automatically add Co-authored-by trailers to selected commits

COMMIT SELECTION STRATEGY:
  • Primary: Shows commits not in origin/main (origin/main..HEAD)
  • Fallback: Shows last 5 commits if origin/main doesn't exist
  • Smart filtering: Only displays commits that don't already have selected LLMs

SUPPORTED AI ASSISTANTS:
  • Claude (Anthropic)     - Co-authored-by: Claude <noreply@anthropic.com>
  • Cursor                 - Co-authored-by: Cursor <cursor@users.noreply.github.com>
  • Gemini (Google)        - Co-authored-by: Gemini <gemini@google.com>
  • ChatGPT (OpenAI)       - Co-authored-by: ChatGPT <noreply@chatgpt.com>
  • GitHub Copilot         - Co-authored-by: Copilot <Copilot@users.noreply.github.com>

COMMIT MESSAGE FORMATTING:
  The script intelligently places Co-authored-by lines following Git conventions:

  WITH Signed-off-by:
    Your commit message here

    Co-authored-by: Claude <noreply@anthropic.com>
    Co-authored-by: Cursor <cursor@users.noreply.github.com>
    Signed-off-by: Your Name <your.email@example.com>

  WITHOUT Signed-off-by:
    Your commit message here

    Co-authored-by: Claude <noreply@anthropic.com>
    Co-authored-by: Cursor <cursor@users.noreply.github.com>

BEHAVIOR:
  ✅ Preserves existing human co-authors
  ✅ Only removes/updates selected LLM co-authors (safe re-runs)
  ✅ Handles both HEAD and historical commits
  ✅ Uses git commit --amend for HEAD commits (fast)
  ✅ Uses git rebase for historical commits (thorough)
  ✅ Maintains proper Git trailer formatting
  ✅ Supports multiple commits and multiple LLMs

SAFETY FEATURES:
  • Validates commit existence before processing
  • Creates temporary files with cleanup
  • Provides clear progress feedback
  • Warns about force push requirements for published commits
  • Graceful error handling with helpful messages

EXAMPLES:
  # Interactive mode: Add Claude and Cursor to recent unpushed commits
  ./hack/add-llm-coauthor

  # Direct mode: Add to specific commits
  ./hack/add-llm-coauthor HEAD HEAD^
  ./hack/add-llm-coauthor abc123f def456g 789hij0

  # Auto mode: Specify LLMs directly
  ./hack/add-llm-coauthor -a cursor,claude HEAD HEAD^
  ./hack/add-llm-coauthor -a gemini,copilot

  # Remove mode: Remove specific LLMs
  ./hack/add-llm-coauthor -r cursor,claude HEAD HEAD^
  ./hack/add-llm-coauthor -r gemini

  # Re-run to add additional LLMs (preserves existing co-authors)
  ./hack/add-llm-coauthor -a claude HEAD

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
EOF
}

# Function to parse and validate LLM names
validate_llms() {
  local llm_string="$1"
  local valid_llms=()
  local invalid_llms=()

  # Split comma-separated LLMs and normalize names
  IFS=',' read -ra llm_array <<<"$llm_string"

  for llm in "${llm_array[@]}"; do
    # Trim whitespace and convert to lowercase for matching
    llm=$(echo "$llm" | tr '[:upper:]' '[:lower:]' | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')

    # Map common variations to full names
    case "$llm" in
    "claude" | "claude (anthropic)")
      valid_llms+=("Claude (Anthropic)")
      ;;
    "cursor")
      valid_llms+=("Cursor")
      ;;
    "gemini" | "gemini (google)")
      valid_llms+=("Gemini (Google)")
      ;;
    "chatgpt" | "chatgpt (openai)")
      valid_llms+=("ChatGPT (OpenAI)")
      ;;
    "copilot" | "github copilot")
      valid_llms+=("GitHub Copilot")
      ;;
    *)
      invalid_llms+=("$llm")
      ;;
    esac
  done

  if [[ ${#invalid_llms[@]} -gt 0 ]]; then
    echo "❌ Error: Unknown LLM(s): ${invalid_llms[*]}" >&2
    echo "💡 Valid options: cursor, claude, gemini, chatgpt, copilot" >&2
    return 1
  fi

  printf '%s\n' "${valid_llms[@]}"
}

# Function to validate and resolve commit references
validate_commits() {
  local commits=("$@")
  local resolved_commits=()

  for commit in "${commits[@]}"; do
    if git rev-parse --verify "$commit^{commit}" >/dev/null 2>&1; then
      local resolved
      resolved=$(git rev-parse --short "$commit")
      resolved_commits+=("$resolved")
    else
      echo "❌ Error: Invalid commit reference '$commit'"
      return 1
    fi
  done

  printf '%s\n' "${resolved_commits[@]}"
}

# Check for required dependencies (only for interactive mode)
if [[ $# -eq 0 ]] && ! command -v fzf >/dev/null 2>&1; then
  echo "❌ Error: fzf is required for interactive mode but not installed"
  echo "Install with: brew install fzf (macOS) or apt install fzf (Ubuntu)"
  echo "💡 Alternatively, specify commits directly: $0 HEAD HEAD^"
  exit 1
fi

# Function to format trailers (Co-authored-by and Signed-off-by) properly
format_trailers() {
  local message="$1"
  shift
  local new_coauthors=("$@")

  # Extract the main commit message (everything before trailers)
  local main_msg
  main_msg=$(echo "$message" | sed '/^Co-authored-by:/,$d' | sed '/^Signed-off-by:/,$d')

  # Remove trailing empty lines from main message
  main_msg=$(echo "$main_msg" | sed ':a;/^\s*$/{$d;N;ba;}')

  # Extract existing Co-authored-by lines (excluding LLM ones we're managing)
  local existing_coauthors
  existing_coauthors=$(echo "$message" | grep "^Co-authored-by:" || true)

  # Extract Signed-off-by lines
  local signed_off_lines
  signed_off_lines=$(echo "$message" | grep "^Signed-off-by:" || true)

  # Build the final message
  local final_msg="$main_msg"

  # Add all co-authors (existing + new) in compact format
  local all_coauthors=()

  # Add existing non-LLM co-authors
  if [[ -n "$existing_coauthors" ]]; then
    while IFS= read -r line; do
      if [[ -n "$line" ]]; then
        # Check if this is not one of our LLM co-authors
        local is_llm_coauthor=false
        for llm_coauthor in "${llm_options[@]}"; do
          if [[ "$line" == "$llm_coauthor" ]]; then
            is_llm_coauthor=true
            break
          fi
        done
        if [[ "$is_llm_coauthor" == "false" ]]; then
          all_coauthors+=("$line")
        fi
      fi
    done <<<"$existing_coauthors"
  fi

  # Add new co-authors
  for coauthor in "${new_coauthors[@]}"; do
    all_coauthors+=("$coauthor")
  done

  # Add trailers section if we have any
  if [[ ${#all_coauthors[@]} -gt 0 || -n "$signed_off_lines" ]]; then
    final_msg="$final_msg
"

    # Add co-authors compactly (no blank lines between them)
    if [[ ${#all_coauthors[@]} -gt 0 ]]; then
      printf -v coauthor_block '%s\n' "${all_coauthors[@]}"
      final_msg="$final_msg
$coauthor_block"
    fi

    # Add signed-off-by lines at the end
    if [[ -n "$signed_off_lines" ]]; then
      final_msg="$final_msg$signed_off_lines"
    fi
  fi

  echo "$final_msg"
}

# Function to remove Co-authored-by lines from a specific commit message
remove_coauthors_from_commit() {
  local commit_hash="$1"
  shift
  local coauthors_to_remove=("$@")

  if [[ ${#coauthors_to_remove[@]} -eq 0 ]]; then
    echo "⚠️  No co-authors to remove"
    return
  fi

  # If it's the HEAD commit, we can use amend
  local head_commit
  head_commit=$(git rev-parse HEAD)
  local full_commit_hash
  full_commit_hash=$(git rev-parse "$commit_hash")

  if [[ "$full_commit_hash" == "$head_commit" ]]; then
    local current_msg
    current_msg=$(git log --format=%B -n 1 HEAD)

    # Remove the specific LLM co-authored-by lines
    local new_msg="$current_msg"
    for coauthor in "${coauthors_to_remove[@]}"; do
      new_msg=$(echo "$new_msg" | grep -v -F "$coauthor")
    done

    # Remove any trailing empty lines
    new_msg=$(echo "$new_msg" | sed '/^$/N;/^\n$/d')

    # Amend the HEAD commit
    echo "$new_msg" | git commit --amend -F -
  else
    # For non-HEAD commits, we need to use rebase
    echo "⚠️  Modifying non-HEAD commit requires interactive rebase."
    echo "🔄 Starting interactive rebase to modify commit $commit_hash"

    # Create a temporary commit message file
    local temp_msg_file
    temp_msg_file=$(mktemp)

    local current_msg
    current_msg=$(git log --format=%B -n 1 "$commit_hash")

    # Remove the specific LLM co-authored-by lines
    local new_msg="$current_msg"
    for coauthor in "${coauthors_to_remove[@]}"; do
      new_msg=$(echo "$new_msg" | grep -v -F "$coauthor")
    done

    # Remove any trailing empty lines
    new_msg=$(echo "$new_msg" | sed '/^$/N;/^\n$/d')

    echo "$new_msg" >"$temp_msg_file"

    # Export the message file path for the rebase script
    export COMMIT_MSG_FILE="$temp_msg_file"
    export TARGET_COMMIT="$commit_hash"

    # Create a rebase script
    local rebase_script
    rebase_script=$(mktemp)

    cat >"$rebase_script" <<'EOF'
#!/bin/bash
# This script will be used by git rebase --exec
current_commit=$(git rev-parse HEAD)
target_full=$(git rev-parse "$TARGET_COMMIT" 2>/dev/null || echo "$TARGET_COMMIT")
if [[ "$current_commit" == "$target_full" ]] || [[ "$current_commit" =~ ^"$TARGET_COMMIT" ]]; then
  git commit --amend -F "$COMMIT_MSG_FILE"
fi
EOF

    chmod +x "$rebase_script"

    # Perform the rebase
    git rebase --exec "$rebase_script" "$commit_hash~1" || {
      echo "❌ Rebase failed. You may need to resolve conflicts manually."
      echo "💡 Run 'git rebase --abort' to cancel or 'git rebase --continue' after resolving."
      rm -f "$temp_msg_file" "$rebase_script"
      return 1
    }

    # Clean up
    rm -f "$temp_msg_file" "$rebase_script"
    unset COMMIT_MSG_FILE TARGET_COMMIT
  fi
}

# Function to add Co-authored-by lines to a specific commit message
add_coauthors_to_commit() {
  local commit_hash="$1"
  shift
  local coauthors=("$@")

  if [[ ${#coauthors[@]} -eq 0 ]]; then
    echo "⚠️  No co-authors to add"
    return
  fi

  # If it's the HEAD commit, we can use amend
  local head_commit
  head_commit=$(git rev-parse HEAD)
  local full_commit_hash
  full_commit_hash=$(git rev-parse "$commit_hash")

  if [[ "$full_commit_hash" == "$head_commit" ]]; then
    local current_msg
    current_msg=$(git log --format=%B -n 1 HEAD)

    # Use the format_trailers function to properly format the message
    local new_msg
    new_msg=$(format_trailers "$current_msg" "${coauthors[@]}")

    # Amend the HEAD commit
    echo "$new_msg" | git commit --amend -F -
  else
    # For non-HEAD commits, we need to use rebase
    echo "⚠️  Modifying non-HEAD commit requires interactive rebase."
    echo "🔄 Starting interactive rebase to modify commit $commit_hash"

    # Create a temporary commit message file
    local temp_msg_file
    temp_msg_file=$(mktemp)

    local current_msg
    current_msg=$(git log --format=%B -n 1 "$commit_hash")

    # Use the format_trailers function to properly format the message
    local new_msg
    new_msg=$(format_trailers "$current_msg" "${coauthors[@]}")

    echo "$new_msg" >"$temp_msg_file"

    # Export the message file path for the rebase script
    export COMMIT_MSG_FILE="$temp_msg_file"
    export TARGET_COMMIT="$commit_hash"

    # Create a rebase script
    local rebase_script
    rebase_script=$(mktemp)

    cat >"$rebase_script" <<'EOF'
#!/bin/bash
# This script will be used by git rebase --exec
current_commit=$(git rev-parse HEAD)
target_full=$(git rev-parse "$TARGET_COMMIT" 2>/dev/null || echo "$TARGET_COMMIT")
if [[ "$current_commit" == "$target_full" ]] || [[ "$current_commit" =~ ^"$TARGET_COMMIT" ]]; then
  git commit --amend -F "$COMMIT_MSG_FILE"
fi
EOF

    chmod +x "$rebase_script"

    # Perform the rebase
    git rebase --exec "$rebase_script" "$commit_hash~1" || {
      echo "❌ Rebase failed. You may need to resolve conflicts manually."
      echo "💡 Run 'git rebase --abort' to cancel or 'git rebase --continue' after resolving."
      rm -f "$temp_msg_file" "$rebase_script"
      return 1
    }

    # Clean up
    rm -f "$temp_msg_file" "$rebase_script"
    unset COMMIT_MSG_FILE TARGET_COMMIT
  fi
}

# LLM options with their Co-authored-by lines
declare -A llm_options=(
  ["Claude (Anthropic)"]="Co-authored-by: Claude <noreply@anthropic.com>"
  ["Cursor"]="Co-authored-by: Cursor <cursor@users.noreply.github.com>"
  ["Gemini (Google)"]="Co-authored-by: Gemini <gemini@google.com>"
  ["ChatGPT (OpenAI)"]="Co-authored-by: ChatGPT <noreply@chatgpt.com>"
  ["GitHub Copilot"]="Co-authored-by: Copilot <Copilot@users.noreply.github.com>"
)

# Function to select commits using fzf
select_commits() {
  local commit_range
  local header_msg

  # Check if origin/main exists
  if git rev-parse --verify origin/main >/dev/null 2>&1; then
    commit_range="origin/main..HEAD"
    header_msg="Select commits not in origin/main (TAB for multiple, ENTER to confirm)"
    echo "📝 Showing commits not in origin/main:" >&2
  else
    commit_range="HEAD~5..HEAD"
    header_msg="Select from last 5 commits (TAB for multiple, ENTER to confirm)"
    echo "📝 origin/main not found, showing last 5 commits:" >&2
  fi

  # Check if there are any commits in the range
  if ! git log --oneline "$commit_range" >/dev/null 2>&1 || [[ $(git log --oneline "$commit_range" | wc -l) -eq 0 ]]; then
    echo "⚠️  No commits found in range $commit_range" >&2
    echo "📝 Falling back to last 5 commits:" >&2
    commit_range="HEAD~5..HEAD"
    header_msg="Select from last 5 commits (TAB for multiple, ENTER to confirm)"
  fi

  git log --oneline "$commit_range" | fzf --multi --preview 'git show --color=always {1}' --preview-window=right:60% \
    --header="$header_msg" \
    --bind="ctrl-d:preview-page-down,ctrl-u:preview-page-up" | cut -d' ' -f1
}

# Function to select LLMs using fzf, filtering based on mode (add/remove)
select_llms() {
  local selected_commits=("$@")
  local available_llms=()
  local existing_llm_coauthors=""

  # Collect all existing LLM co-authors from selected commits
  for commit in "${selected_commits[@]}"; do
    local commit_coauthors
    commit_coauthors=$(git log --format=%B -n 1 "$commit" | grep "^Co-authored-by:" || true)
    existing_llm_coauthors="$existing_llm_coauthors$commit_coauthors"$'\n'
  done

  if [[ "$remove_mode" == "true" ]]; then
    # Remove mode: only show LLMs that are present in the commits
    for llm in "${!llm_options[@]}"; do
      local llm_coauthor="${llm_options[$llm]}"
      if grep -q "$llm_coauthor" <<< "$existing_llm_coauthors"; then
        available_llms+=("$llm")
      fi
    done

    if [[ ${#available_llms[@]} -eq 0 ]]; then
      echo "❌ No LLM co-authors found in the selected commits!" >&2
      return 1
    fi

    echo "🗑️  Select AI assistants to REMOVE (use TAB for multiple selection, ENTER to confirm):" >&2
    printf '%s\n' "${available_llms[@]}" | sort | fzf --multi \
      --header="Select AI assistants to REMOVE (TAB for multiple, ENTER to confirm)"
  else
    # Add mode: filter out LLMs that already have co-authors in any of the selected commits
    for llm in "${!llm_options[@]}"; do
      local llm_coauthor="${llm_options[$llm]}"
      if ! grep -q "$llm_coauthor" <<< "$existing_llm_coauthors"; then
        available_llms+=("$llm")
      else
        echo "⚠️  Skipping '$llm' - already present in selected commit(s)" >&2
      fi
    done

    if [[ ${#available_llms[@]} -eq 0 ]]; then
      echo "❌ All LLMs already have co-authors in the selected commits!" >&2
      return 1
    fi

    echo "🤖 Select AI assistants used (use TAB for multiple selection, ENTER to confirm):" >&2
    printf '%s\n' "${available_llms[@]}" | sort | fzf --multi \
      --header="Select AI assistants (TAB for multiple, ENTER to confirm)"
  fi
}

# Parse command line arguments
auto_llms=""
remove_llms=""
remove_mode=false
commit_args=()

while [[ $# -gt 0 ]]; do
  case $1 in
  -h | --help)
    show_help
    exit 0
    ;;
  -a | --auto)
    if [[ "$remove_mode" == "true" ]]; then
      echo "❌ Error: Cannot use -a and -r flags together"
      exit 1
    fi
    if [[ -n "${2:-}" && ! "$2" =~ ^- ]]; then
      auto_llms="$2"
      shift 2
    else
      echo "❌ Error: -a flag requires LLM names (e.g., -a cursor,claude)"
      exit 1
    fi
    ;;
  -r | --remove)
    if [[ -n "$auto_llms" ]]; then
      echo "❌ Error: Cannot use -a and -r flags together"
      exit 1
    fi
    remove_mode=true
    if [[ -n "${2:-}" && ! "$2" =~ ^- ]]; then
      remove_llms="$2"
      shift 2
    else
      # Allow -r without arguments for interactive selection
      shift
    fi
    ;;
  -*)
    echo "❌ Error: Unknown option $1"
    echo "💡 Usage: $0 [-a llm1,llm2] [-r llm1,llm2] [commit1] [commit2] ..."
    echo "💡 Use $0 --help for detailed information"
    exit 1
    ;;
  *)
    commit_args+=("$1")
    shift
    ;;
  esac
done

# Main execution
if [[ "$remove_mode" == "true" ]]; then
  echo "🗑️  Remove LLM Co-authors from Git Commits"
else
  echo "🚀 Add LLM Co-authors to Git Commits"
fi
echo "===================================="

# Determine commit selection mode
if [[ ${#commit_args[@]} -eq 0 ]]; then
  # Interactive commit selection: use fzf to select commits
  echo "📝 Interactive commit selection: Select commits using fzf"
  mapfile -t selected_commits < <(select_commits)
  if [[ ${#selected_commits[@]} -eq 0 ]]; then
    echo "❌ No commits selected. Exiting."
    exit 1
  fi
else
  # Direct commit selection: use provided commit arguments
  echo "🎯 Direct commit selection: Using provided commit references"
  if ! mapfile -t selected_commits < <(validate_commits "${commit_args[@]}"); then
    echo "❌ Commit validation failed. Exiting."
    exit 1
  fi
fi

echo "✅ Selected ${#selected_commits[@]} commit(s): ${selected_commits[*]}"

# Determine LLM selection mode
if [[ "$remove_mode" == "true" ]]; then
  if [[ -n "$remove_llms" ]]; then
    # Auto remove mode: use provided LLM list
    echo "🗑️  Auto LLM removal: Using provided LLMs"
    if ! mapfile -t selected_llms < <(validate_llms "$remove_llms"); then
      echo "❌ LLM validation failed. Exiting."
      exit 1
    fi
  else
    # Interactive remove mode: use fzf to select LLMs to remove
    echo "🗑️  Interactive LLM removal: Choose AI assistants to remove"
    mapfile -t selected_llms < <(select_llms "${selected_commits[@]}")
    if [[ ${#selected_llms[@]} -eq 0 ]]; then
      echo "❌ No AI assistants selected for removal. Exiting."
      exit 1
    fi
  fi
  echo "✅ Selected ${#selected_llms[@]} AI assistant(s) to REMOVE: ${selected_llms[*]}"
else
  if [[ -n "$auto_llms" ]]; then
    # Auto LLM selection: use provided LLM list
    echo "🤖 Auto LLM selection: Using provided LLMs"
    if ! mapfile -t selected_llms < <(validate_llms "$auto_llms"); then
      echo "❌ LLM validation failed. Exiting."
      exit 1
    fi
  else
    # Interactive LLM selection: use fzf to select LLMs
    echo "🤖 Interactive LLM selection: Choose AI assistants"
    mapfile -t selected_llms < <(select_llms "${selected_commits[@]}")
    if [[ ${#selected_llms[@]} -eq 0 ]]; then
      echo "❌ No AI assistants selected. Exiting."
      exit 1
    fi
  fi
  echo "✅ Selected ${#selected_llms[@]} AI assistant(s): ${selected_llms[*]}"
fi

# Convert LLM names to Co-authored-by lines
coauthor_lines=()
for llm in "${selected_llms[@]}"; do
  if [[ -n "${llm_options[$llm]:-}" ]]; then
    coauthor_lines+=("${llm_options[$llm]}")
  else
    echo "⚠️  Warning: Unknown LLM '$llm', skipping..."
  fi
done

if [[ ${#coauthor_lines[@]} -eq 0 ]]; then
  echo "❌ No valid co-author lines generated. Exiting."
  exit 1
fi

# Process each commit
for commit in "${selected_commits[@]}"; do
  echo ""
  echo "🔄 Processing commit: $commit"
  git log --oneline -1 "$commit"

  # Check if commit exists and is reachable
  if ! git cat-file -e "$commit^{commit}" 2>/dev/null; then
    echo "❌ Commit $commit not found or not reachable"
    continue
  fi

  if [[ "$remove_mode" == "true" ]]; then
    # Remove co-authors from the commit
    remove_coauthors_from_commit "$commit" "${coauthor_lines[@]}"
    echo "✅ Removed co-authors from commit $commit"
  else
    # Add co-authors to the commit
    add_coauthors_to_commit "$commit" "${coauthor_lines[@]}"
    echo "✅ Added co-authors to commit $commit"
  fi
done

echo ""
if [[ "$remove_mode" == "true" ]]; then
  echo "🎉 All selected commits have been updated - AI co-authors removed!"
else
  echo "🎉 All selected commits have been updated with AI co-authors!"
fi
echo "💡 Note: If you've modified commits that were already pushed, you'll need to force push."

exit 0
