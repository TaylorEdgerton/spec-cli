#!/usr/bin/env sh
set -e

BIN="spec"
SOURCE=${1:-}
DEFAULT_DIR="${XDG_BIN_HOME:-$HOME/.local/bin}"
PROFILE="$HOME/.profile"
PATH_MARKER_BEGIN="# >>> spec-cli PATH >>>"
PATH_MARKER_END="# <<< spec-cli PATH <<<"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
if [ -n "${SPEC_CONFIG_HOME:-}" ]; then
	config_dir=$SPEC_CONFIG_HOME
elif [ "$os" = "darwin" ]; then
	config_dir="$HOME/Library/Application Support/spec"
else
	config_dir="${XDG_CONFIG_HOME:-$HOME/.config}/spec"
fi

if [ -z "$SOURCE" ] || [ ! -f "$SOURCE" ]; then
	echo "usage: scripts/install-dev.sh /path/to/spec" >&2
	exit 1
fi

existing=$(command -v "$BIN" 2>/dev/null || true)
if [ -n "$existing" ]; then
	target_dir=$(CDPATH= cd "$(dirname "$existing")" && pwd)
	target="$target_dir/$(basename "$existing")"
	echo "installing development build over $target"
else
	target_dir=$DEFAULT_DIR
	target="$target_dir/$BIN"
	echo "installing development build to $target"
fi

mkdir -p "$target_dir"
mkdir -p "$config_dir"
temporary=$(mktemp "$target_dir/.spec-dev.XXXXXX")
trap 'rm -f "$temporary"' EXIT HUP INT TERM
cp "$SOURCE" "$temporary"
chmod 0755 "$temporary"
mv "$temporary" "$target"
trap - EXIT HUP INT TERM

case ":$PATH:" in
	*":$target_dir:"*) ;;
	*)
		if ! grep -Fq "$PATH_MARKER_BEGIN" "$PROFILE" 2>/dev/null; then
			{
				echo
				echo "$PATH_MARKER_BEGIN"
				echo "export PATH=\"$target_dir:\$PATH\""
				echo "$PATH_MARKER_END"
			} >>"$PROFILE"
		fi
		echo "added $target_dir to PATH in $PROFILE; restart your shell"
		;;
esac

"$target" --version
echo "configuration folder: $config_dir"
