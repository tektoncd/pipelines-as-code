{{- $ref := .Get 0 -}}
{{- $parts := split $ref "#" -}}
{{- $path := strings.TrimSuffix ".md" (index $parts 0) -}}
{{- $fragment := "" -}}
{{- if gt (len $parts) 1 -}}{{- $fragment = printf "#%s" (index $parts 1) -}}{{- end -}}
{{- $lookup := $path -}}
{{- if not (hasPrefix $lookup "/") -}}{{- $lookup = path.Join (printf "/%s" (path.Dir .Page.File.Path)) $lookup -}}{{- end -}}
{{- $target := .Page.GetPage $lookup -}}
{{- if $target -}}
  {{- with partial "markdown/url.html" (dict "page" $target) -}}{{ . }}{{- else -}}{{ $target.RelPermalink }}{{- end -}}{{ $fragment }}
{{- else -}}
  {{- .RelRef (dict "path" $lookup "outputFormat" "markdown") -}}{{ $fragment }}
{{- end -}}
