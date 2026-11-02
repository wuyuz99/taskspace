#!/bin/sh
# Installs tmux and zellij on a CI runner. zellij is pinned on both systems, so
# a red job on one is not a different zellij.
set -eu
version="${ZELLIJ_VERSION:?set ZELLIJ_VERSION, such as v0.45.1}"
case "$(uname -s)/$(uname -m)" in
  Linux/x86_64) target=x86_64-unknown-linux-musl ;;
  Darwin/arm64) target=aarch64-apple-darwin ;;
  Darwin/x86_64) target=x86_64-apple-darwin ;;
  *) echo "no zellij build chosen for $(uname -s)/$(uname -m)" >&2; exit 1 ;;
esac
if [ "$(uname -s)" = Linux ]; then
  sudo apt-get update && sudo apt-get install -y tmux
else
  brew install tmux
fi
curl -fsSL "https://github.com/zellij-org/zellij/releases/download/$version/zellij-$target.tar.gz" \
  | sudo tar -xz -C /usr/local/bin zellij
zellij --version
