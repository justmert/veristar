#!/bin/sh
# installs the pinned supply-chain tools into a directory,
# each from its github release and checked against the release's own
# checksum file before it is kept. used by the workflows and for local runs:
#
#   scripts/supply-chain/install-tools.sh <dir> [syft] [trivy] [cosign] [gitleaks]
#     [actionlint] [shellcheck] [promtool] [uv]
#
# linux amd64 only, which is what the runners and the dev host are.
set -eu

if [ $# -lt 2 ]; then
	echo "usage: $0 <dir> tool..." >&2
	exit 2
fi
dest="$1"
shift
mkdir -p "$dest"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

fetch() {
	# url, checksum file url, asset name
	curl -fsSL --retry 5 -o "$work/$3" "$1"
	curl -fsSL --retry 5 -o "$work/sums" "$2"
	(cd "$work" && grep " $3\$" sums | sha256sum -c - >/dev/null) || {
		echo "checksum mismatch for $3" >&2
		exit 1
	}
}

for tool in "$@"; do
	case "$tool" in
	syft)
		v=1.57.1
		base="https://github.com/anchore/syft/releases/download/v$v"
		fetch "$base/syft_${v}_linux_amd64.tar.gz" "$base/syft_${v}_checksums.txt" "syft_${v}_linux_amd64.tar.gz"
		tar -xzf "$work/syft_${v}_linux_amd64.tar.gz" -C "$work" syft
		install -m 0755 "$work/syft" "$dest/syft"
		;;
	trivy)
		v=0.76.2
		base="https://github.com/aquasecurity/trivy/releases/download/v$v"
		fetch "$base/trivy_${v}_Linux-64bit.tar.gz" "$base/trivy_${v}_checksums.txt" "trivy_${v}_Linux-64bit.tar.gz"
		tar -xzf "$work/trivy_${v}_Linux-64bit.tar.gz" -C "$work" trivy
		install -m 0755 "$work/trivy" "$dest/trivy"
		;;
	cosign)
		v=3.1.4
		base="https://github.com/sigstore/cosign/releases/download/v$v"
		fetch "$base/cosign-linux-amd64" "$base/cosign_checksums.txt" "cosign-linux-amd64"
		install -m 0755 "$work/cosign-linux-amd64" "$dest/cosign"
		;;
	gitleaks)
		v=8.30.1
		base="https://github.com/gitleaks/gitleaks/releases/download/v$v"
		fetch "$base/gitleaks_${v}_linux_x64.tar.gz" "$base/gitleaks_${v}_checksums.txt" "gitleaks_${v}_linux_x64.tar.gz"
		tar -xzf "$work/gitleaks_${v}_linux_x64.tar.gz" -C "$work" gitleaks
		install -m 0755 "$work/gitleaks" "$dest/gitleaks"
		;;
	actionlint)
		v=1.7.12
		base="https://github.com/rhysd/actionlint/releases/download/v$v"
		fetch "$base/actionlint_${v}_linux_amd64.tar.gz" "$base/actionlint_${v}_checksums.txt" "actionlint_${v}_linux_amd64.tar.gz"
		tar -xzf "$work/actionlint_${v}_linux_amd64.tar.gz" -C "$work" actionlint
		install -m 0755 "$work/actionlint" "$dest/actionlint"
		;;
	shellcheck)
		# the shellcheck project publishes no checksum file; this is the sha256 digest
		# github records for the release asset (read 2026-11-13), so a
		# replaced asset still fails the check
		v=0.11.0
		a="shellcheck-v$v.linux.x86_64.tar.gz"
		curl -fsSL --retry 5 -o "$work/$a" "https://github.com/koalaman/shellcheck/releases/download/v$v/$a"
		(cd "$work" && echo "b7af85e41cc99489dcc21d66c6d5f3685138f06d34651e6d34b42ec6d54fe6f6  $a" | sha256sum -c - >/dev/null) || {
			echo "checksum mismatch for $a" >&2
			exit 1
		}
		tar -xzf "$work/$a" -C "$work" "shellcheck-v$v/shellcheck"
		install -m 0755 "$work/shellcheck-v$v/shellcheck" "$dest/shellcheck"
		;;
	promtool)
		# the pinned prometheus version, so rule checks match the server
		v=3.15.1
		base="https://github.com/prometheus/prometheus/releases/download/v$v"
		fetch "$base/prometheus-$v.linux-amd64.tar.gz" "$base/sha256sums.txt" "prometheus-$v.linux-amd64.tar.gz"
		tar -xzf "$work/prometheus-$v.linux-amd64.tar.gz" -C "$work" "prometheus-$v.linux-amd64/promtool"
		install -m 0755 "$work/prometheus-$v.linux-amd64/promtool" "$dest/promtool"
		;;
	uv)
		# make ansible-venv builds the controller venv with uv
		v=0.13.9
		a=uv-x86_64-unknown-linux-gnu.tar.gz
		fetch "https://github.com/astral-sh/uv/releases/download/$v/$a" "https://github.com/astral-sh/uv/releases/download/$v/$a.sha256" "$a"
		tar -xzf "$work/$a" -C "$work"
		install -m 0755 "$work/uv-x86_64-unknown-linux-gnu/uv" "$dest/uv"
		;;
	*)
		echo "unknown tool: $tool" >&2
		exit 2
		;;
	esac
	echo "installed $tool to $dest"
done
