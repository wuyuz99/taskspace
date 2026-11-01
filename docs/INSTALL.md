# Installation

[Back to the README](../README.md)

## Prebuilt binary

Download the latest release for Linux or macOS (amd64 or arm64):

```bash
os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m | sed -e s/x86_64/amd64/ -e s/aarch64/arm64/)
tmp=$(mktemp -d)
curl -fsSL "https://github.com/wuyuz99/taskspace/releases/latest/download/tspace_${os}_${arch}.tar.gz" |
  tar -xzf - -C "$tmp" --strip-components=1
sudo install -m 0755 "$tmp/tspace" /usr/local/bin/tspace   # or anywhere on PATH
tspace --version
```

Run the same commands again to upgrade. No Go toolchain is needed.

Each archive holds the binary, the license, the changelog and the docs.
`checksums.txt` on the [releases page](https://github.com/wuyuz99/taskspace/releases)
lists each archive's SHA-256. Each archive also has a versioned name, such as
`tspace_v0.3.0_linux_amd64.tar.gz`, for installing a specific release from that
page.

## With Go

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

## From source

```bash
git clone https://github.com/wuyuz99/taskspace.git && cd taskspace
go build -o tspace ./cmd/tspace
sudo install -m 0755 tspace /usr/local/bin/tspace
```
