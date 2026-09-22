# Security Policy

volcano-vgpu-device-plugin is a subproject of [HAMi](https://github.com/Project-HAMi/HAMi).
See the [HAMi security policy](https://github.com/Project-HAMi/HAMi/blob/master/SECURITY.md)
for the project's general security posture.

## Supported Versions

Only the latest released version receives security fixes.

## Reporting a Vulnerability

Please **do not** open a public issue for a security vulnerability. Report it
privately through [GitHub Security Advisories](https://github.com/Project-HAMi/volcano-vgpu-device-plugin/security/advisories/new).

Include a clear description, reproduction steps, and the potential impact.
Response times may vary with weekends, holidays, and time zones, but
maintainers aim to reply within 5 working days.

## Verifying Release Images

Release images are signed with [cosign](https://github.com/sigstore/cosign)
keyless signing by the `Build Release Image` workflow. The signature is bound
to the workflow's GitHub OIDC identity and recorded in the Sigstore
transparency log, so no key has to be distributed. Images up to and including
`v1.12.0` were published before signing was added and carry no signature.

Verify an image and the identity that built it:

```shell
cosign verify docker.io/projecthami/volcano-vgpu-device-plugin:<version> \
  --certificate-identity https://github.com/Project-HAMi/volcano-vgpu-device-plugin/.github/workflows/release-image-build.yml@refs/tags/<version> \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

`<version>` is the release tag, for example `v1.13.0`. The command succeeds only
if the image was signed by this repository's release workflow running for that
tag. For the `latest` tag, match any release tag instead:

```shell
cosign verify docker.io/projecthami/volcano-vgpu-device-plugin:latest \
  --certificate-identity-regexp '^https://github\.com/Project-HAMi/volcano-vgpu-device-plugin/\.github/workflows/release-image-build\.yml@refs/tags/v[0-9]+\.[0-9]+\.[0-9]+$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## Dependency Management

Go dependencies are declared in `go.mod` and pinned in `go.sum`. The in-container
library comes from the `libvgpu` git submodule, pinned to a HAMi-core commit.
Dependabot opens update pull requests for Go modules, GitHub Actions and the
Docker base image every week, and Dependabot alerts provide dependency scanning
(SCA) for the default branch.

- Vulnerabilities rated critical or high in a dependency must be fixed, or the
  dependency replaced, before the next release.
- A dependency under a license incompatible with Apache-2.0 must be removed or
  replaced before it is merged.
- Open SCA findings rated critical or high must be resolved before release.
  Maintainers run `govulncheck ./...` on the release commit to confirm this.

Dependency review (SCA) must check every pull request for known
vulnerabilities and malicious dependencies. The `dependency-review` check is
required on `main`, so a pull request that introduces such a finding is blocked
from merging until it is fixed. A finding can be waived only when it is declared
non-exploitable in the pull request with a justification, and its advisory ID is
added to `allow-ghsas` in `.github/workflows/dependency-review.yaml`.

## Secrets Management

Credentials used by CI, such as the registry tokens used by the release
workflows, are stored only as GitHub Actions encrypted secrets and are never
committed to the repository. Secret scanning with push protection is enabled,
and `.gitignore` excludes common credential files. Maintainers rotate a
credential immediately if it may have been exposed, and when a maintainer with
access to it leaves the project.
