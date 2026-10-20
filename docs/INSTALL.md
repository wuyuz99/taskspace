# Installation

[Back to the README](../README.md)

## Install

With Go 1.26 or later:

```bash
go install github.com/wuyuz99/taskspace/cmd/tspace@latest
```

This puts `tspace` in `$(go env GOPATH)/bin`, usually `~/go/bin`; make sure
that is on your `PATH`.

Without Go, download a binary for Linux or macOS (amd64 or arm64) from the
[releases page](https://github.com/wuyuz99/taskspace/releases):

```bash
tar -xzf tspace_v0.1.0_linux_amd64.tar.gz
sudo install -m 0755 tspace_v0.1.0_linux_amd64/tspace /usr/local/bin/tspace   # or anywhere on PATH
tspace --version
```

From source:

```bash
git clone https://github.com/wuyuz99/taskspace.git && cd taskspace
go build -o tspace ./cmd/tspace
sudo install -m 0755 tspace /usr/local/bin/tspace
```
