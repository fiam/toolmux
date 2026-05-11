package policy

const StarterPolicy = `version: 1
default: deny

roles:
  reader:
    allow:
      - id: allow-read
        provider: "*"
        remote_effects: ["read", "none"]
        local_effects: ["none"]
  operator:
    extends: ["reader"]
    allow:
      - id: allow-linear-create
        provider: "linear"
        resources: ["issue"]
        actions: ["create"]
    deny:
      - id: deny-destructive
        risks: ["destructive"]

bindings:
  - role: operator
    profiles: ["default"]
    accounts: ["*"]
`
