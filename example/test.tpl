Here be dragons

Client: {{ .Client }}
Vars: {{ .Vars }}

foo: {{ .Vars.foo }}

Fnord:
{{ range .Vars.fnord }}
   {{- . }}
{{ end -}}
