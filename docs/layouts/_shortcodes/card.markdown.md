{{- $link := .Get "link" -}}
{{- $href := $link -}}
{{- if and $link (not (or (hasPrefix $link "http://") (hasPrefix $link "https://") (hasPrefix $link "#") (hasPrefix $link "mailto:"))) -}}
  {{- $path := strings.TrimSuffix ".md" $link -}}
  {{- if not (hasPrefix $path "/") -}}
    {{- $base := strings.TrimSuffix ".md" .Page.File.Path -}}
    {{- if or (eq $base "_index") (hasSuffix $base "/_index") -}}{{- $base = strings.TrimSuffix "_index" $base -}}{{- end -}}
    {{- $path = path.Join (printf "/%s" $base) $path -}}
  {{- end -}}
  {{- with .Page.GetPage $path -}}
    {{- $href = partial "markdown/url.html" (dict "page" .) -}}
  {{- else -}}
    {{- $href = $.RelRef (dict "path" $path "outputFormat" "markdown") -}}
  {{- end -}}
{{- end -}}

- [{{ .Get "title" }}]({{ $href }}){{ with .Get "subtitle" }} — {{ . }}{{ end }}
