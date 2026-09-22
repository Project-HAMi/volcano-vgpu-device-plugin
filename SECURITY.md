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

## Secrets Management

Credentials used by CI, such as the registry tokens used by the release
workflows, are stored only as GitHub Actions encrypted secrets and are never
committed to the repository. Secret scanning with push protection is enabled,
and `.gitignore` excludes common credential files. Maintainers rotate a
credential immediately if it may have been exposed, and when a maintainer with
access to it leaves the project.
