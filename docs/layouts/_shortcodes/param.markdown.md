{{- $name := .Get "name" -}}
{{- $type := .Get "type" -}}
{{- $required := .Get "required" -}}
{{- $default := .Get "default" -}}
{{- $id := .Get "id" | default (printf "param-%s" ($name | urlize)) -}}
{{- $depth := 0 -}}
{{- $ancestor := .Parent -}}
{{- range seq 6 -}}
  {{- if $ancestor -}}{{- $depth = add $depth 1 -}}{{- $ancestor = $ancestor.Parent -}}{{- end -}}
{{- end -}}
{{- $heading := strings.Repeat (int (math.Min (add 3 $depth) 6)) "#" -}}
<a id="{{ $id }}"></a>

{{ $heading }} `{{ $name }}`{{ with $type }} — `{{ . }}`{{ end }}{{ if eq $required "true" }} — required{{ end }}{{ with $default }} — default: `{{ . }}`{{ end }}

{{ .InnerDeindent }}
