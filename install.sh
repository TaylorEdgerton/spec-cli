#!/usr/bin/env sh
set -e

REPOSITORY="${SPEC_REPOSITORY:-TaylorEdgerton/spec-cli}"
BIN="spec"
DIR="${XDG_BIN_HOME:-$HOME/.local/bin}"
PROFILE="$HOME/.profile"
PATH_MARKER_BEGIN="# >>> spec-cli PATH >>>"
PATH_MARKER_END="# <<< spec-cli PATH <<<"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in
	x86_64 | amd64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	*) echo "unsupported arch: $arch" >&2; exit 1 ;;
esac
case "$os" in
	linux | darwin) ;;
	*) echo "unsupported os: $os (use install.ps1 on Windows)" >&2; exit 1 ;;
esac

asset="$BIN-$os-$arch"
url="https://github.com/$REPOSITORY/releases/latest/download/$asset"
mkdir -p "$DIR"
temporary=$(mktemp "$DIR/.spec.XXXXXX")
trap 'rm -f "$temporary"' EXIT HUP INT TERM
echo "downloading $url"
curl -fsSL "$url" -o "$temporary"
chmod +x "$temporary"
mv "$temporary" "$DIR/$BIN"

case ":$PATH:" in
	*":$DIR:"*) ;;
	*)
		if ! grep -Fq "$PATH_MARKER_BEGIN" "$PROFILE" 2>/dev/null; then
			{
				echo
				echo "$PATH_MARKER_BEGIN"
				echo "export PATH=\"$DIR:\$PATH\""
				echo "$PATH_MARKER_END"
			} >>"$PROFILE"
		fi
		echo "added $DIR to PATH in $PROFILE; restart your shell"
		;;
esac

"$DIR/$BIN" --version
