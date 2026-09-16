{{- $depth := 0 -}}
{{- $ancestor := .Parent -}}
{{- range seq 6 -}}
  {{- if $ancestor -}}{{- $depth = add $depth 1 -}}{{- $ancestor = $ancestor.Parent -}}{{- end -}}
{{- end -}}
{{- $heading := strings.Repeat (int (math.Min (add 3 $depth) 6)) "#" -}}
{{ $heading }} {{ .Get "label" | default "Show Fields" }}

{{ .InnerDeindent }}
