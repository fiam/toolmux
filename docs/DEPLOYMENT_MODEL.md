# Deployment Model

Last updated: 2026-05-09

Toolmux uses a split between the public OSS product repository and private
deployment infrastructure.

## Public OSS Repository

This repository contains product source and public artifacts:

```text
cmd/toolmux/                  # CLI source
cmd/toolmuxd/                 # server daemon source
internal/                     # shared CLI/server packages
Dockerfile                    # generic toolmuxd OCI image
docs/                         # product, self-hosting, and provider docs
```

The public repo should publish:

1. Signed `toolmux` CLI binaries through GitHub Releases.
2. A Homebrew tap for the CLI.
3. Generic Ko-built `toolmuxd` Linux amd64/arm64 images through GHCR.
4. SBOMs, checksums, signatures, and provenance for release artifacts.
5. Generic self-hosting instructions for users who bring their own provider
   OAuth apps.

The public repo must not contain:

1. Provider client secrets.
2. Production AWS account identifiers.
3. DNS, certificate, or production routing state.
4. Terraform/Pulumi/CDK for Toolmux's hosted production deployment.
5. Production rate-limit, abuse, billing, monitoring, or alerting internals.
6. Lambda-specific deployment wrappers that assume Toolmux's AWS account.

## Private Infrastructure Repository

The private repo owns Toolmux's hosted deployment.

Suggested private repo responsibilities:

```text
toolmux-infra/
  aws/
  lambda/
  dns/
  secrets/
  monitoring/
  pipelines/
```

It should contain:

1. AWS Lambda, API Gateway, Lambda Function URL, or other hosting definitions.
2. ECR publishing or image promotion configuration.
3. Secrets Manager or SSM Parameter Store references.
4. Provider OAuth app client ids and client secrets as deployment secrets.
5. Domain and certificate configuration for hosted `toolmuxd`.
6. Production monitoring, alerting, abuse controls, and access policies.

## Artifact Boundary

The private repo consumes public artifacts from this repo:

```text
toolmux CLI release -> Homebrew tap / GitHub Release
toolmuxd GHCR image -> private AWS deployment
```

The public `toolmuxd` image is a generic Linux HTTP server image published as
`ghcr.io/fiam/toolmuxd:<tag>` for amd64 and arm64. The private deployment may
adapt it for AWS Lambda with an AWS-specific wrapper, Lambda Web Adapter,
Lambda Function URL, or API Gateway. That adaptation belongs in the private
repo because it is deployment-specific rather than product source.

## Secret Boundary

Self-hosters must create their own provider OAuth apps and supply their own
secrets:

```text
NOTION_CLIENT_ID=...
NOTION_CLIENT_SECRET=...
ATLASSIAN_CLIENT_ID=...
ATLASSIAN_CLIENT_SECRET=...
SLACK_CLIENT_ID=...
SLACK_CLIENT_SECRET=...
```

Toolmux's hosted deployment uses Toolmux-owned provider apps and secrets from
private deployment infrastructure. Those secrets must never be committed to the
OSS repo.

## AWS Direction

Toolmux's hosted deployment may use AWS Lambda, but that deployment is not part
of this OSS repo. The OSS repo should keep `toolmuxd` as a portable HTTP daemon
that can run locally, in Docker, on a VPS, on ECS, or behind any reverse proxy.

AWS-specific notes for the private repo:

1. AWS Lambda supports container images for functions.
2. Lambda Function URLs and API Gateway can expose HTTPS endpoints.
3. AWS Secrets Manager can provide provider OAuth secrets to Lambda.

## References

1. AWS Lambda container images:
   https://docs.aws.amazon.com/lambda/latest/dg/go-image.html
2. AWS Lambda Function URLs:
   https://docs.aws.amazon.com/lambda/latest/dg/urls-configuration.html
3. AWS Secrets Manager with Lambda:
   https://docs.aws.amazon.com/lambda/latest/dg/with-secrets-manager.html
