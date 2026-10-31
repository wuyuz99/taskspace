# Installation

[Back to the README](../README.md)

## Install

With Go 1.26 or later:

```bash
go install github.com/wuyuz99/taskspace/cmd/tspace@latest
```

This installs the newest release and puts `tspace` in `$(go env GOPATH)/bin`,
usually `~/go/bin`; make sure that is on your `PATH`. Run the same command again
to upgrade.

[CHANGELOG.md](../CHANGELOG.md) lists what each release changed. These docs follow the `main` branch, so a feature merged since the
last release is described here before it is released. To try it early, install
`@main` instead of `@latest`.

Without Go, download a binary for Linux or macOS (amd64 or arm64) from the
[releases page](https://github.com/wuyuz99/taskspace/releases):

```bash
VERSION=v0.2.0   # the release you downloaded
tar -xzf tspace_${VERSION}_linux_amd64.tar.gz
sudo install -m 0755 tspace_${VERSION}_linux_amd64/tspace /usr/local/bin/tspace   # or anywhere on PATH
tspace --version
```

Each archive holds the binary, the license, the changelog and the docs.
`checksums.txt` on the release page lists each archive's SHA-256.

From source:

```bash
git clone https://github.com/wuyuz99/taskspace.git && cd taskspace
go build -o tspace ./cmd/tspace
sudo install -m 0755 tspace /usr/local/bin/tspace
```
