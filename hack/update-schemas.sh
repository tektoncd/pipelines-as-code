#!/usr/bin/env bash
# Copyright 2025 The Tekton Authors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#
# Imported from tektoncd/pipeline/hack/update-schemas.sh and vibed/adapted for
# PAAC.

set -o errexit
set -o nounset
set -o pipefail

OLDGOFLAGS="${GOFLAGS:-}"
GOFLAGS=""

SCRIPT_DIR=$(dirname "${BASH_SOURCE[0]}")
CRD_PATH="${SCRIPT_DIR}/../config"
API_PATH="${SCRIPT_DIR}/../pkg/apis"
TEMP_DIR_LOGS=$(mktemp -d)

echo "=== Generating CRD schemas with OpenAPI validation ==="

# List of CRDs to process
CRD_FILES=(
  "${CRD_PATH}/300-repositories.yaml"
  # Add more CRD files here as they are created
)

for FILENAME in "${CRD_FILES[@]}"; do
  BASENAME=$(basename "$FILENAME")
  echo "Generating OpenAPI schema for $FILENAME"

  # Extract group from the CRD file
  GROUP=$(grep -E '^  group:' $FILENAME)
  GROUP=${GROUP#"  group: "}
  API_SUBDIR=${GROUP%".tekton.dev"}

  TEMP_DIR=$(mktemp -d)
  cp -p $FILENAME $TEMP_DIR/.
  LOG_FILE=$TEMP_DIR_LOGS/log-schema-generation-$(basename $FILENAME)

  echo "  Processing API group: $GROUP, subdir: $API_SUBDIR"

  counter=0 limit=5
  while [ "$counter" -lt "$limit" ]; do
    set +e
    # Use controller-gen to generate the schema and deepcopy
    go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.17.1 \
      object \
      crd:crdVersions=v1 \
      output:crd:artifacts:config=$TEMP_DIR \
      paths=$API_PATH/$API_SUBDIR/... >$LOG_FILE 2>&1
    rc=$?
    set -e

    if [ $rc -eq 0 ]; then
      echo "  ✅ Successfully generated schema"

      # Find the auto-generated CRD file
      if command -v yq >/dev/null 2>&1 && yq --version | grep -q "mikefarah/yq"; then
        echo "  🔄 Syncing schema from temporary CRD to $BASENAME"

        # Find the auto-generated CRD file in the temp directory
        AUTO_GENERATED_CRD=$(find $TEMP_DIR -name "${GROUP}_*.yaml")

        if [ -f "$AUTO_GENERATED_CRD" ]; then
          # Extract schema from auto-generated CRD and apply it to the original CRD
          yq eval '.spec.versions[0].schema' "$AUTO_GENERATED_CRD" >/tmp/schema.yaml
          yq eval -i '.spec.versions[0].schema = load("/tmp/schema.yaml")' "$FILENAME"
          echo "  ✅ Schema successfully synced to $BASENAME"

          # Clean up temporary schema file
          rm -f /tmp/schema.yaml
        else
          echo "  ⚠️  Warning: Auto-generated CRD not found in temporary directory"
        fi
      else
        echo "  ⚠️  Warning: mikefarah/yq not available, cannot automatically sync schema"
        echo "  Manual action required: Copy schema from auto-generated CRD to $BASENAME"
      fi

      go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.17.1 \
        object \
        crd:crdVersions=v1 \
        output:crd:artifacts:config=$CRD_PATH \
        paths=$API_PATH/$API_SUBDIR/... >/dev/null 2>&1

      # Find and delete the auto-generated CRD file from the config directory
      AUTO_GENERATED_CRD=$(find $CRD_PATH -name "${GROUP}_*.yaml")
      if [ -f "$AUTO_GENERATED_CRD" ]; then
        echo "  🗑️  Removing auto-generated CRD file: $(basename "$AUTO_GENERATED_CRD")"
        rm -f "$AUTO_GENERATED_CRD"
      fi

      break
    fi

    # Check if we can proceed despite errors
    if grep -q 'exit status 1' $LOG_FILE; then
      echo "  ⚠️  Warning: Encountered errors during schema generation"
      echo "  📝 Check $LOG_FILE for details"
      # Attempt to extract and display useful error information
      ERROR_MSG=$(grep -A 3 'Error:' $LOG_FILE 2>/dev/null || echo "Unknown error")
      echo "  Error summary: $ERROR_MSG"
      break
    fi

    counter=$((counter + 1))
    if [ $counter -eq $limit ]; then
      echo "  ❌ Failed to generate CRD schema after $limit attempts"
      echo "  📝 Check $LOG_FILE for details"
      cat ${LOG_FILE}
      exit 1
    fi

    echo "  Retrying (attempt $counter of $limit)..."
    sleep 1
  done

  # Validate the generated CRD
  echo "  Validating generated CRD"
  if command -v kubectl >/dev/null 2>&1; then
    # Try using the newer "kubectl server-side validation" approach
    if kubectl explain crd &>/dev/null; then
      if kubectl apply --server-side --dry-run=server --validate=strict -f "$FILENAME" >/dev/null 2>&1; then
        echo "  ✅ CRD validation successful using server-side validation"
      else
        echo "  ⚠️  Warning: Server-side CRD validation failed, validation may not be complete"
      fi
    else
      echo "  ⚠️  Warning: kubectl API resources not available, skipping validation"
    fi
  else
    echo "  ⚠️  Warning: kubectl not found, skipping validation"
  fi

  rm -rf $TEMP_DIR
done

echo "=== Schema generation complete ==="
echo "Generated schemas saved to: $CRD_PATH"
echo "OpenAPI schema has been successfully added to the numbered CRD files"
echo "Auto-generated reference CRD files have been removed as requested"
echo "Log files available at: $TEMP_DIR_LOGS"

GOFLAGS="${OLDGOFLAGS}"
