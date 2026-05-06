package policy

const StarterPolicy = `version: 1
default: deny

roles:
  reader:
    allow:
      - id: allow-read
        provider: "*"
        actions: ["read", "list", "search", "status"]
  operator:
    extends: ["reader"]
    allow:
      - id: allow-jira-write
        provider: "jira"
        resources: ["issue", "comment"]
        actions: ["create", "update"]
      - id: allow-linear-write
        provider: "linear"
        resources: ["issue", "comment"]
        actions: ["create", "update"]
    deny:
      - id: deny-gmail-send
        provider: "gmail"
        actions: ["send"]
      - id: deny-drive-delete-share
        provider: "google-drive"
        actions: ["delete", "share"]

bindings:
  - role: operator
    profiles: ["default"]
    accounts: ["*"]
`
