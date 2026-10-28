#!/usr/bin/env bash
set -euf

export TARGET_REPO_CONTROLLER=${TARGET_REPO_CONTROLLER:-ghcr.io/tektoncd/pipelines-as-code/pipelines-as-code-controller}
export TARGET_REPO_WATCHER=${TARGET_REPO_WATCHER:-ghcr.io/tektoncd/pipelines-as-code/pipelines-as-code-watcher}
export TARGET_REPO_WEBHOOK=${TARGET_REPO_WEBHOOK:-ghcr.io/tektoncd/pipelines-as-code/pipelines-as-code-webhook}
export TARGET_BRANCH=${TARGET_BRANCH:-main}
export TARGET_NAMESPACE=${TARGET_NAMESPACE:-pipelines-as-code}
export TARGET_OPENSHIFT=${TARGET_OPENSHIFT:-""}
export TARGET_PAC_VERSION=${PAC_VERSION:-"devel"}

TMP=$(mktemp /tmp/.mm.XXXXXX)
clean() { rm -f ${TMP}; }
trap clean EXIT

MODE=${1:-""}

if [[ -n ${MODE} && ${MODE} == ko ]]; then
  tmpfile=$(mktemp /tmp/.mm.XXXXXX)
  clean() { rm -f ${tmpfile}; }
  trap clean EXIT
  ko resolve -f config/ >${tmpfile}

  if [[ ${TARGET_OPENSHIFT} != "" ]]; then
    ko resolve -f config/openshift >>${tmpfile}
  fi

  files="${tmpfile}"
else
  files=$(find config -maxdepth 1 -name '*.yaml' | sort -n)

  if [[ ${TARGET_OPENSHIFT} != "" ]]; then
    files="${files} $(find config/openshift -maxdepth 1 -name '*.yaml' | sort -n)"
  fi
fi

for file in ${files}; do
  sed -e '/^$/d' -e '/^#/d' ${file} | head -1 | grep -q -- "---" || echo -e "---\n"
  sed -r -e "s,(.*image:.*)ko://github.com/tektoncd/pipelines-as-code/cmd/pipelines-as-code-controller.*,\1${TARGET_REPO_CONTROLLER}:${TARGET_BRANCH}\"," \
    -r -e "s,(.*image:.*)ko://github.com/tektoncd/pipelines-as-code/cmd/pipelines-as-code-watcher.*,\1${TARGET_REPO_WATCHER}:${TARGET_BRANCH}\"," \
    -r -e "s,(.*image:.*)ko://github.com/tektoncd/pipelines-as-code/cmd/pipelines-as-code-webhook.*,\1${TARGET_REPO_WEBHOOK}:${TARGET_BRANCH}\"," \
    -e "s/(namespace: )\w+.*/\1${TARGET_NAMESPACE}/g" \
    -e "s,app.kubernetes.io/version:.*,app.kubernetes.io/version: \"${TARGET_PAC_VERSION}\"," \
    -e "s/Copyright[ ]*[0-9]{4}/Copyright $(date "+%Y")/" \
    -e "/kind: Namespace$/ { n;n;s/name: .*/name: ${TARGET_NAMESPACE}/;}" \
    -e "s/\"devel\"/\"${TARGET_PAC_VERSION}\"/" \
    ${file} >${TMP}

  # Remove openshift stuff apiGroups if we are not targetting openshift...
  [[ -z ${TARGET_OPENSHIFT} ]] && {
    sed -ir '/^[ ]*- apiGroups:.*route.openshift.io/,/verbs.*/d' ${TMP}
  }

  echo "" >>${TMP}
  tail -1 ${TMP} | grep -q "^$" && sed -i '$d' ${TMP} >>/tmp/aaaa
  cat ${TMP}
done
