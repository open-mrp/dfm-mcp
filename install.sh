#!/bin/sh
# Installs the latest dfm-mcp release on macOS or Linux from the archives goreleaser attaches to each GitHub release, checking it against the release's checksums.
#
#   curl -fsSL https://raw.githubusercontent.com/open-mrp/dfm-mcp/main/install.sh | sh
#
# DFM_MCP_VERSION pins a release (v1.2.3); DFM_MCP_INSTALL_DIR picks the directory, by default ~/.local/bin.
set -eu

repo="open-mrp/dfm-mcp"
install_dir="${DFM_MCP_INSTALL_DIR:-$HOME/.local/bin}"

if [ -t 1 ]; then
	bold=$(printf '\033[1m'); dim=$(printf '\033[2m'); green=$(printf '\033[32m'); red=$(printf '\033[31m'); reset=$(printf '\033[0m')
else
	bold=""; dim=""; green=""; red=""; reset=""
fi

step() { printf '%s→%s %s\n' "$dim" "$reset" "$1"; }
ok() { printf '%s✓%s %s\n' "$green" "$reset" "$1"; }
fail() { printf '%s✗ %s%s\n' "$red" "$1" "$reset" >&2; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || fail "dfm-mcp's installer needs $1"; }
need curl
need tar
need uname

case "$(uname -s)" in
	Darwin) os=darwin ;;
	Linux) os=linux ;;
	*) fail "No installer for $(uname -s); take a binary from https://github.com/$repo/releases/latest" ;;
esac
case "$(uname -m)" in
	x86_64 | amd64) arch=amd64 ;;
	arm64 | aarch64) arch=arm64 ;;
	*) fail "No build for $(uname -m); take a binary from https://github.com/$repo/releases/latest" ;;
esac

printf '\n%sdfm-mcp%s %s· the DFM check engine as a local MCP server%s\n\n' "$bold" "$reset" "$dim" "$reset"

# The latest release is read from the redirect GitHub serves for it, which needs no API token and has no rate limit.
if [ -n "${DFM_MCP_VERSION:-}" ]; then
	tag="$DFM_MCP_VERSION"
else
	step "Finding the latest release"
	tag=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$repo/releases/latest" | sed 's|.*/||')
	case "$tag" in v*) ;; *) fail "Could not find the latest release of $repo" ;; esac
fi
version="${tag#v}"
archive="dfm-mcp_${version}_${os}_${arch}.tar.gz"
base="https://github.com/$repo/releases/download/$tag"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

step "Downloading dfm-mcp $tag for $os/$arch"
curl -fsSL -o "$tmp/$archive" "$base/$archive" || fail "Could not download $base/$archive"
curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt" || fail "Could not download $base/checksums.txt"

step "Verifying the checksum"
want=$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")
[ -n "$want" ] || fail "$archive is not in the release's checksums"
if command -v sha256sum >/dev/null 2>&1; then
	got=$(sha256sum "$tmp/$archive" | awk '{ print $1 }')
else
	got=$(shasum -a 256 "$tmp/$archive" | awk '{ print $1 }')
fi
[ "$want" = "$got" ] || fail "Checksum mismatch for $archive"

step "Installing into $install_dir"
tar -xzf "$tmp/$archive" -C "$tmp" dfm-mcp
mkdir -p "$install_dir"
mv "$tmp/dfm-mcp" "$install_dir/dfm-mcp"
chmod 755 "$install_dir/dfm-mcp"

printf '\n'
ok "${bold}dfm-mcp $tag${reset} installed to $install_dir/dfm-mcp"

# The literal $PATH is what the user should paste.
# shellcheck disable=SC2016
case ":$PATH:" in
	*":$install_dir:"*) ;;
	*) printf '\n%s is not on your PATH. Add it with:\n\n  export PATH="%s:$PATH"\n' "$install_dir" "$install_dir" ;;
esac

cat <<EOF

Add it to your MCP client:

  ${dim}# Claude Code${reset}
  claude mcp add --scope user dfm -- dfm-mcp
  ${dim}# Codex${reset}
  codex mcp add dfm -- dfm-mcp
  ${dim}# Gemini CLI${reset}
  gemini mcp add dfm dfm-mcp

  ${dim}# Cursor: add to ~/.cursor/mcp.json${reset}
  { "mcpServers": { "dfm": { "command": "dfm-mcp" } } }
  ${dim}# opencode: add to ~/.config/opencode/opencode.json${reset}
  { "mcp": { "dfm": { "type": "local", "command": ["dfm-mcp"] } } }

EOF
