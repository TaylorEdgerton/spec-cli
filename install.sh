#!/usr/bin/env sh
set -e

REPOSITORY="${SPEC_REPOSITORY:-TaylorEdgerton/spec-cli}"
BIN="spec"
DIR="${XDG_BIN_HOME:-$HOME/.local/bin}"
PROFILE="$HOME/.profile"
PATH_MARKER_BEGIN="# >>> spec-cli PATH >>>"
PATH_MARKER_END="# <<< spec-cli PATH <<<"

version_is_higher() {
	awk -v current="$1" -v candidate="$2" '
		function component(value, index, parts) {
			sub(/^v/, "", value)
			split(value, parts, ".")
			sub(/[^0-9].*$/, "", parts[index])
			return parts[index] + 0
		}
		BEGIN {
			for (index = 1; index <= 3; index++) {
				currentPart = component(current, index)
				candidatePart = component(candidate, index)
				if (candidatePart > currentPart) exit 0
				if (candidatePart < currentPart) exit 1
			}
			exit 1
		}
	'
}

confirm_update() {
	case "${SPEC_INSTALL_YES:-}" in
		1 | true | TRUE | yes | YES) return 0 ;;
	esac
	if [ ! -r /dev/tty ]; then
		echo "update available; rerun interactively or set SPEC_INSTALL_YES=1" >&2
		return 1
	fi
	printf "spec %s is installed; %s is available. Update? [y/N] " "$1" "$2" >/dev/tty
	IFS= read -r reply </dev/tty
	case "$reply" in
		y | Y | yes | YES) return 0 ;;
		*) return 1 ;;
	esac
}

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

if [ -n "${SPEC_CONFIG_HOME:-}" ]; then
	config_dir=$SPEC_CONFIG_HOME
elif [ "$os" = "darwin" ]; then
	config_dir="$HOME/Library/Application Support/spec"
else
	config_dir="${XDG_CONFIG_HOME:-$HOME/.config}/spec"
fi

mkdir -p "$DIR"
mkdir -p "$config_dir"

release_api="https://api.github.com/repos/$REPOSITORY/releases/latest"
if [ -n "${GITHUB_TOKEN:-}" ]; then
	release_json=$(curl -fsSL -H "Accept: application/vnd.github+json" -H "X-GitHub-Api-Version: 2026-03-10" -H "Authorization: Bearer $GITHUB_TOKEN" -H "User-Agent: spec-cli-installer" "$release_api")
else
	release_json=$(curl -fsSL -H "Accept: application/vnd.github+json" -H "X-GitHub-Api-Version: 2026-03-10" -H "User-Agent: spec-cli-installer" "$release_api")
fi
latest_version=$(printf '%s\n' "$release_json" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | sed -n '1p')
if [ -z "$latest_version" ]; then
	echo "could not determine the latest spec release" >&2
	exit 1
fi

asset="$BIN-$os-$arch"
url="https://github.com/$REPOSITORY/releases/download/$latest_version/$asset"
target="$DIR/$BIN"
install_release=true
if [ -f "$target" ]; then
	current_version=$("$target" --version 2>/dev/null | awk 'NR == 1 { print $NF }' || true)
	current_version=${current_version:-unknown}
	if version_is_higher "$current_version" "$latest_version"; then
		if ! confirm_update "$current_version" "$latest_version"; then
			install_release=false
			echo "update cancelled"
		fi
	elif version_is_higher "$latest_version" "$current_version"; then
		install_release=false
		echo "installed spec $current_version is newer than latest release $latest_version; no update needed"
	else
		install_release=false
		echo "spec $current_version is already the latest release"
	fi
fi

if [ "$install_release" = true ]; then
	temporary=$(mktemp "$DIR/.spec.XXXXXX")
	trap 'rm -f "$temporary"' EXIT HUP INT TERM
	echo "downloading $url"
	curl -fsSL "$url" -o "$temporary"
	chmod +x "$temporary"
	mv "$temporary" "$target"
fi

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

if [ -x "$target" ]; then
	"$target" --version
fi
echo "configuration folder: $config_dir"
