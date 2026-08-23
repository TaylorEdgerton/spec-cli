#!/usr/bin/env sh
set -eu

repo_root=$(git rev-parse --show-toplevel 2>/dev/null) || {
	echo "release: Git repository not found" >&2
	exit 1
}
cd "$repo_root"

branch=$(git branch --show-current)
if [ "$branch" != "main" ]; then
	echo "release: current branch must be main" >&2
	exit 1
fi

if [ -n "$(git status --porcelain)" ]; then
	echo "release: working tree is not clean" >&2
	echo "Commit or remove all changes before release." >&2
	exit 1
fi

remote_url=$(git remote get-url --push origin 2>/dev/null) || {
	echo "release: origin remote not found" >&2
	exit 1
}
case "$remote_url" in
	https://github.com/* | git@github.com:* | ssh://git@github.com/*) ;;
	*)
		echo "release: origin is not a GitHub repository: $remote_url" >&2
		exit 1
		;;
esac

latest=$(git tag --list 'v[0-9]*' --sort=-v:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | sed -n '1p' || true)
suggested="v0.1.0"
if [ -n "$latest" ]; then
	core=${latest#v}
	major=${core%%.*}
	rest=${core#*.}
	minor=${rest%%.*}
	patch=${rest#*.}
	suggested="v$major.$minor.$((patch + 1))"
fi

printf 'Release version [%s]: ' "$suggested"
IFS= read -r version || exit 1
version=${version:-$suggested}
case "$version" in
	v*) ;;
	*) version="v$version" ;;
esac

if ! printf '%s\n' "$version" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$'; then
	echo "release: version must use a form such as v1.2.3 or v1.2.3-rc.1" >&2
	exit 1
fi

if git show-ref --verify --quiet "refs/tags/$version"; then
	echo "release: local tag already exists: $version" >&2
	exit 1
fi
if git ls-remote --exit-code --tags origin "refs/tags/$version" >/dev/null 2>&1; then
	echo "release: remote tag already exists: $version" >&2
	exit 1
fi

commit=$(git rev-parse --short HEAD)
subject=$(git log -1 --pretty=%s)
echo "Release $version from $commit: $subject"
printf 'Verify, tag, and push this release? [y/N] '
IFS= read -r answer || exit 1
case "$answer" in
	y | Y | yes | YES | Yes) ;;
	*)
		echo "release cancelled"
		exit 0
		;;
esac

./scripts/verify.sh
git tag -a "$version" -m "$version"

if ! git push origin main "refs/tags/$version"; then
	echo "release: push failed; local tag $version remains" >&2
	echo "After you fix the problem, run: git push origin main refs/tags/$version" >&2
	exit 1
fi

echo "release: pushed $version"
echo "GitHub Actions will build and publish the release."
