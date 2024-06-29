# oictl

Primitive CLI for Open WebUI 

```
go build -o oictl
```
```
export OI_TOKEN=<API key from Open WebUI>
```
```
./oictl <path-to-definition(s)>
```
Current supported definitions

"Documents" example

```
kind: Documents
metadata:
  name: my-docs
spec:
  sources:
    - source: git@github.com:<org|user>/<repo>.git
      dir:
        - <subdir>/
      extensions:
        - .md
        - .pdf
    - source: https://url-to-file/README.md
    - source: ../../../dir/file.yaml
    - source: file.md
```

"Model" example
```
kind: Model
metadata:
  name: my-model
spec:
  base_model_id: llama3:latest
  meta:
    description: "Description"
    capabilities:
      vision: false
    suggestion_prompts: []
    knowledge:
      - tags: my-docs # <name of collection / Documents definition>
  params: {}
```
