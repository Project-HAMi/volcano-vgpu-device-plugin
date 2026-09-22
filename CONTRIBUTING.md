# Contributing

Welcome to volcano-vgpu-device-plugin!

This repository is a subproject of [HAMi](https://github.com/Project-HAMi/HAMi).
The general contributor workflow, code of conduct, and community expectations of
the main project apply here as well; see the
[HAMi contributing guide](https://github.com/Project-HAMi/HAMi/blob/master/CONTRIBUTING.md).

## Submitting changes

1. Search the [open issues](https://github.com/Project-HAMi/volcano-vgpu-device-plugin/issues)
   first. For anything beyond a small fix, open an issue describing the problem
   or enhancement before writing code.
2. Fork the repository and create a topic branch from `main`.
3. Make your change. Keep it focused on one problem.
4. Build and test locally:

   ```shell
   git submodule update --init --recursive
   go build ./...
   go test ./pkg/...
   ```

5. Sign off every commit (see below) and push to your fork.
6. Open a pull request against `main` using the pull request template.
7. A maintainer listed in [OWNERS](OWNERS) reviews the change. At least one
   approving review and passing checks are required before merge.

## Sign your commits

Every commit must carry a Developer Certificate of Origin (DCO) sign-off:

```shell
git commit -s
```

This appends a `Signed-off-by` line with your name and email to the commit
message.

## Hardware validation

Changes affecting device allocation or in-container isolation must be validated
on real GPU hardware. Record in the pull request what was tested, the device
type, and the driver version.

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
