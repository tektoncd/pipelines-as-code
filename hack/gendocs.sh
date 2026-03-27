#!/usr/bin/env bash
set -eufo pipefail
dir=/var/www/docs
keeplast=10

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
# Keep only the last ${keeplast} major versions
output=$(for i in "${!hashmap[@]}"; do
  echo "$i"
done | sort -V | tail -${keeplast} | while read major; do
  echo v"${hashmap[$major]}"
done | sort -rV | tr "\n" " ")
allversiontags="nightly,${output// /,}"

# replace_version_selector replaces the version dropdown and its JavaScript
# with a static link to the versions page in all generated HTML files.
replace_version_selector() {
  local target_dir=$1
  find "${target_dir}" -name '*.html' -type f -exec perl -0777 -i -pe '
    s{<select[^>]*handleVersion[^>]*>.*?</select>.*?<script[^>]*>.*?handleVersion.*?</script>}
     {<a href="/versions.html" style="text-decoration:none;padding:4px 10px;border:1px solid \x23ccc;border-radius:4px;display:inline-block">Other Versions</a>}gs;
  ' {} \;
}

# fix_deprecated_hugo_apis patches Hugo API calls that were removed in Hugo
# v0.128+. Only needed for older release branches; harmless on newer ones.
fix_deprecated_hugo_apis() {
  find docs -name '*.html' -type f -print0 | xargs -0 sed -i \
    -e 's/resources\.ToCSS/css.Sass/g' \
    -e 's/\.Sites\.First/.Sites.Default/g' \
    -e 's/\.Site\.IsMultiLingual/hugo.IsMultilingual/g'
}

for i in $output; do
  version=${versiondir}/${i}
  [[ -d ${version} ]] && continue
  git checkout -B gendoc origin/release-$i
  echo ${allversiontags} >docs/content/ALLVERSIONS
  find docs/content -name '*.md' -print0 | xargs -0 sed -i 's,/images/,../../../images/,g'

  # Fix BookLogo path for old versions that used config.toml
  [[ -f docs/config.toml ]] && sed -i 's,BookLogo = "/,BookLogo = ",' docs/config.toml

  fix_deprecated_hugo_apis

  mkdir -p ${version}
  hugo -d ${version} -s docs -b https://docs.pipelinesascode.com/$i

  replace_version_selector "${version}"

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
hugo -d "${nightly_dir}" -s docs -b https://docs.pipelinesascode.com/nightly

replace_version_selector "${nightly_dir}"

git reset --hard
git clean -f .

latest=${output%% *}

# Generate versions.html with links to all available versions
cat <<EOF >${versiondir}/versions.html
<!DOCTYPE html>
<html lang="en">
  <head>
    <title>Pipelines as Code - All Versions</title>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <style>
      :root {
        --bg: #0d1117;
        --surface: #161b22;
        --border: #30363d;
        --text: #e6edf3;
        --text-muted: #8b949e;
        --accent: #58a6ff;
        --green: #3fb950;
        --green-bg: rgba(63,185,80,0.15);
        --yellow: #d29922;
        --yellow-bg: rgba(210,153,34,0.15);
      }
      @media (prefers-color-scheme: light) {
        :root {
          --bg: #f6f8fa;
          --surface: #ffffff;
          --border: #d0d7de;
          --text: #1f2328;
          --text-muted: #656d76;
          --accent: #0969da;
          --green: #1a7f37;
          --green-bg: rgba(26,127,55,0.1);
          --yellow: #9a6700;
          --yellow-bg: rgba(154,103,0,0.1);
        }
      }
      * { box-sizing: border-box; margin: 0; padding: 0; }
      body {
        font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
        background: var(--bg);
        color: var(--text);
        min-height: 100vh;
        display: flex;
        justify-content: center;
        padding: 60px 20px;
      }
      .container { max-width: 520px; width: 100%; }
      .header {
        display: flex;
        align-items: center;
        gap: 12px;
        margin-bottom: 32px;
      }
      .logo {
        width: 40px; height: 40px;
        background: var(--accent);
        border-radius: 10px;
        display: flex; align-items: center; justify-content: center;
        flex-shrink: 0;
      }
      .logo svg { width: 22px; height: 22px; fill: white; }
      h1 { font-size: 22px; font-weight: 600; }
      h1 span { display: block; font-size: 13px; color: var(--text-muted); font-weight: 400; margin-top: 2px; }
      .version-list { display: flex; flex-direction: column; gap: 8px; }
      .version-item {
        display: flex; align-items: center; justify-content: space-between;
        padding: 14px 16px;
        background: var(--surface);
        border: 1px solid var(--border);
        border-radius: 10px;
        text-decoration: none;
        color: var(--text);
        transition: border-color 0.15s, box-shadow 0.15s;
      }
      .version-item:hover {
        border-color: var(--accent);
        box-shadow: 0 0 0 1px var(--accent);
      }
      .version-name { font-weight: 500; font-size: 15px; }
      .badge {
        font-size: 11px; font-weight: 600;
        padding: 3px 8px; border-radius: 20px;
        text-transform: uppercase; letter-spacing: 0.5px;
      }
      .badge-latest { background: var(--green-bg); color: var(--green); }
      .badge-dev { background: var(--yellow-bg); color: var(--yellow); }
      .arrow {
        color: var(--text-muted);
        transition: transform 0.15s;
        flex-shrink: 0; margin-left: 8px;
      }
      .version-item:hover .arrow { transform: translateX(3px); color: var(--accent); }
      .version-left { display: flex; align-items: center; gap: 10px; }
    </style>
  </head>
  <body>
    <div class="container">
      <div class="header">
        <div class="logo">
          <svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg">
            <path d="M13.983 11.078h2.119a.186.186 0 0 0 .186-.186V9.006a.186.186 0 0 0-.186-.186h-2.119a.186.186 0 0 0-.187.186v1.886c0 .103.084.186.187.186m-2.954-5.43h2.118a.186.186 0 0 0 .187-.186V3.574a.186.186 0 0 0-.187-.186h-2.118a.186.186 0 0 0-.187.186v1.888c0 .103.083.186.187.186m0 5.43h2.118a.186.186 0 0 0 .187-.186V9.006a.186.186 0 0 0-.187-.186h-2.118a.186.186 0 0 0-.187.186v1.886c0 .103.083.186.187.186m-2.965 0h2.118a.186.186 0 0 0 .187-.186V9.006a.186.186 0 0 0-.187-.186H8.064a.186.186 0 0 0-.187.186v1.886c0 .103.084.186.187.186"/>
          </svg>
        </div>
        <h1>Pipelines as Code<span>Documentation Versions</span></h1>
      </div>
      <div class="version-list">
        <a href="/${latest}/" class="version-item">
          <div class="version-left">
            <span class="version-name">${latest}</span>
            <span class="badge badge-latest">latest</span>
          </div>
          <svg class="arrow" width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2"><path d="M6 3l5 5-5 5"/></svg>
        </a>
        <a href="/nightly/" class="version-item">
          <div class="version-left">
            <span class="version-name">nightly</span>
            <span class="badge badge-dev">development</span>
          </div>
          <svg class="arrow" width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2"><path d="M6 3l5 5-5 5"/></svg>
        </a>
EOF

for v in ${output}; do
  [[ "${v}" == "${latest}" ]] && continue
  cat <<ITEM >>${versiondir}/versions.html
        <a href="/${v}/" class="version-item">
          <div class="version-left">
            <span class="version-name">${v}</span>
          </div>
          <svg class="arrow" width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2"><path d="M6 3l5 5-5 5"/></svg>
        </a>
ITEM
done

cat <<EOF >>${versiondir}/versions.html
      </div>
    </div>
  </body>
</html>
EOF

# Redirect root to latest version
cat <<EOF >${versiondir}/index.html
<!DOCTYPE html>
<html lang="en">
  <head>
    <title>Pipelines as Code Documentation</title>
    <meta http-equiv="refresh" content="0;URL='./${latest}'" />
  </head>
  <body>
    <p>Redirecting to <a href="./${latest}">${latest}</a>…</p>
  </body>
</html>
EOF
