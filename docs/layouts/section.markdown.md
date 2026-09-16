{{- $content := partial "markdown/render.html" . -}}
{{- $targetPath := printf "%s.md" (strings.TrimPrefix "/" .Path) -}}
{{- with resources.FromString $targetPath $content -}}
  {{- $noop := .Publish -}}
{{- end -}}
{{- $content -}}
