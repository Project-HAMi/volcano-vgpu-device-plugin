# Contributing

Welcome to volcano-vgpu-device-plugin!

This repository is a subproject of [HAMi](https://github.com/Project-HAMi/HAMi).
The general contributor workflow, code of conduct, and community expectations of
the main project apply here as well; see the
[HAMi contributing guide](https://github.com/Project-HAMi/HAMi/blob/master/CONTRIBUTING.md)
and the [code of conduct](CODE_OF_CONDUCT.md).

## Submitting changes

1. Search the [open issues](https://github.com/Project-HAMi/volcano-vgpu-device-plugin/issues)
   first. For anything beyond a small fix, open an issue describing the problem
   or enhancement before writing code.
2. Fork the repository and create a topic branch from `main`.
3. Make your change. Keep it focused on one problem.
4. Run the tests (see [Running tests](#running-tests)).
5. Sign off every commit (see below) and push to your fork.
6. Open a pull request against `main` using the pull request template.

## Review requirements

Every change reaches `main` through a pull request. A pull request is merged
only when:

- at least one maintainer listed in [OWNERS](OWNERS) who is not the author has
  approved it, and a new push dismisses earlier approvals;
- all review conversations are resolved;
- the DCO and build checks pass;
- it is scoped to one problem and includes tests for changed behavior where the
  code is unit testable;
- new dependencies meet the [dependency policy](SECURITY.md#dependency-management).

## Sign your commits

Every commit must carry a Developer Certificate of Origin (DCO) sign-off:

```shell
git commit -s
```

This appends a `Signed-off-by` line with your name and email to the commit
message.

## Running tests

Unit tests run on every pull request through the build workflow and can be run
locally:

```shell
git submodule update --init --recursive
go build ./...
go vet ./...
go test ./...
```

## Maintaining tests

A change to behavior must come with a new or updated unit test that fails
without the change. A test that no longer matches the code is fixed or removed
in the same pull request that changes the code, never skipped silently.

## Hardware validation

Changes affecting device allocation or in-container isolation must be validated
on real GPU hardware. Record in the pull request what was tested, the device
type, and the driver version.

## Roles and permissions

Roles follow the [HAMi community membership](https://github.com/Project-HAMi/community/blob/main/community-membership.md)
process. Maintainers are listed in [OWNERS](OWNERS). A contributor must be
reviewed and approved by the existing maintainers before being granted
reviewer, approver, write or admin access to this repository.

## AI assistance

If you used any kind of AI assistance, disclose it in the pull request along
with its extent (for example docs only vs. code generation). You must be able
to explain every line of your change; if a maintainer asks how a change works
and the author cannot explain it, the pull request is closed. Do not list AI
as a co-author in commit trailers.

## Reporting security issues

Do not open public issues for vulnerabilities. See [SECURITY.md](SECURITY.md).

## Getting help

Ask questions in [GitHub issues](https://github.com/Project-HAMi/volcano-vgpu-device-plugin/issues)
or through the channels listed in the [HAMi community repository](https://github.com/Project-HAMi/community).
