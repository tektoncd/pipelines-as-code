#!/usr/bin/env bash
# Copyright 2024 Chmouel Boudjnah <chmouel@chmouel.com>
set -eufo pipefail
dir=/var/www/docs

mkdir -p ${dir}
if [[ -d ${dir}/git ]]; then
  cd ${dir}/git
  git reset --hard
  git pull --all
  git clean -f .
else
  git clone --tags https:///github.com/tektoncd/pipelines-as-code.git ${dir}/git
  cd ${dir}/git
fi

versiondir=${dir}/versions
mkdir -p ${versiondir}

declare -A hashmap=()
for i in $(git tag -l | grep '^v' | sort -V); do
  version=${i//v/}
  if [[ ${version} =~ ^([0-9]+\.[0-9]+)\.[0-9]+$ ]]; then
    major_version=${BASH_REMATCH[1]}
  fi
  hashmap["$major_version"]=$version
done
output=$(for i in "${!hashmap[@]}"; do
  echo v"${hashmap[$i]}"
done | sort -rV | tr "\n" " ")
allversiontags="nightly,${output// /,}"

for i in $output; do
  version=${versiondir}/${i}
  [[ -d ${version} ]] && continue
  git checkout -B gendoc origin/release-$i
  echo ${allversiontags} >docs/content/ALLVERSIONS
  find docs/content -name '*.md' -print0 | xargs -0 sed -i 's,/images/,../../../images/,g'
  sed -i 's#window.location = "https://release-" + elm.value.replace(/\./g, "-") + ".pipelines-as-code.pages.dev" + path;#window.location = "https://docs.pipelinesascode.com/" + elm.value + "/" + path.split("/").slice(2).join("/");#' docs/layouts/_partials/custom/footer.html
  mkdir -p ${version}
  hugo -d ${version} -s docs -b https://docs.pipelinesascode.com/$i
  git reset --hard
  git clean -f .
done

# Generate nightly from main branch
nightly_dir="${versiondir}/nightly"
mkdir -p "${nightly_dir}"
git checkout main
git pull origin main
echo "${allversiontags}" >docs/content/ALLVERSIONS
find docs/content -name '*.md' -print0 | xargs -0 sed -i 's,/images/,../../../images/,g'
sed -i 's#window.location = "https://release-" + elm.value.replace(/\./g, "-") + ".pipelines-as-code.pages.dev" + path;#window.location = "https://docs.pipelinesascode.com/" + elm.value + "/" + path.split("/").slice(2).join("/");#' docs/layouts/_partials/custom/footer.html
hugo -d "${nightly_dir}" -s docs -b https://docs.pipelinesascode.com/nightly
git reset --hard
git clean -f .

latest=${output/ *//}
# generate simple HTML
cat <<EOF >${versiondir}/index.html
 <html xmlns="http://www.w3.org/1999/xhtml">
  <head>
    <title>OpenShift Pipelines as Code versions Documentation</title>
    <meta http-equiv="refresh" content="0;URL='./${latest}'" />
  </head>
  <body>
    <p>This page has moved to a <a href="./${latest}">
      ./${latest}</a>.</p>
  </body>
</html>
EOF
