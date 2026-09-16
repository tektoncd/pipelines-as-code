{{- $type := lower (.Get "type" | default "note") -}}
{{- if eq $type "info" -}}{{- $type = "note" -}}{{- end -}}
{{- if eq $type "error" -}}{{- $type = "caution" -}}{{- end -}}
{{- $type = upper $type -}}
{{- $inner := .InnerDeindent -}}
> [!{{ $type }}]
>
{{- if $inner }}
> {{ replace $inner "\n" "\n> " }}
{{- end }}
