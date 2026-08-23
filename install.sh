#!/bin/sh

set -eu
umask 077

repository="https://github.com/kriss-spy/plugman"
version="${PLUGMAN_VERSION:-@PLUGMAN_VERSION@}"
install_dir="${PLUGMAN_INSTALL_DIR:-${HOME}/.local/bin}"
staged=""

usage() {
  cat <<'EOF'
Usage: install.sh [--version <version>] [--bin-dir <directory>]

Installs Plugman for the current user. The latest release is used by default.
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --version)
      [ "$#" -ge 2 ] || { printf '%s\n' "install.sh: --version requires a value" >&2; exit 2; }
      version=$2
      shift 2
      ;;
    --bin-dir)
      [ "$#" -ge 2 ] || { printf '%s\n' "install.sh: --bin-dir requires a value" >&2; exit 2; }
      install_dir=$2
      shift 2
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      printf 'install.sh: unknown option %s\n' "$1" >&2
      exit 2
      ;;
  esac
done

command -v curl >/dev/null 2>&1 || { printf '%s\n' "install.sh: curl is required" >&2; exit 1; }

case "$install_dir" in
  /*) ;;
  *) printf 'install.sh: install directory must be absolute: %s\n' "$install_dir" >&2; exit 1 ;;
esac

case "$(uname -s)" in
  Linux|linux) os="linux" ;;
  Darwin|darwin) os="darwin" ;;
  *) printf 'install.sh: unsupported operating system: %s\n' "$(uname -s)" >&2; exit 1 ;;
esac

case "$(uname -m)" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) printf 'install.sh: unsupported architecture: %s\n' "$(uname -m)" >&2; exit 1 ;;
esac

if [ "$version" = "@PLUGMAN_VERSION@" ]; then
  version=""
fi
if [ -z "$version" ]; then
  version=$(curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 --connect-timeout 10 --max-time 60 --retry 3 --retry-delay 1 --output /dev/null --write-out '%{url_effective}' "$repository/releases/latest")
  version=${version%/}
  version=${version##*/}
fi
case "$version" in
  v*) ;;
  *) version="v${version}" ;;
esac
printf '%s\n' "$version" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$' || {
  printf 'install.sh: invalid version: %s\n' "$version" >&2
  exit 1
}

asset="plugman_${version}_${os}_${arch}"
download_base="${repository}/releases/download/${version}"
temporary_dir=$(mktemp -d "${TMPDIR:-/tmp}/plugman-install.XXXXXX")
cleanup() {
  rm -rf "$temporary_dir"
  if [ -n "$staged" ]; then
    rm -f "$staged"
  fi
}
trap cleanup EXIT HUP INT TERM

curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 --connect-timeout 10 --max-time 60 --retry 3 --retry-delay 1 --output "$temporary_dir/checksums.txt" "$download_base/checksums.txt"
curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 --connect-timeout 10 --max-time 60 --retry 3 --retry-delay 1 --output "$temporary_dir/$asset" "$download_base/$asset"

expected=$(awk -v asset="$asset" '$2 == asset { print $1 }' "$temporary_dir/checksums.txt")
[ -n "$expected" ] || { printf 'install.sh: checksum is missing for %s\n' "$asset" >&2; exit 1; }
if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$temporary_dir/$asset" | awk '{ print $1 }')
elif command -v shasum >/dev/null 2>&1; then
  actual=$(shasum -a 256 "$temporary_dir/$asset" | awk '{ print $1 }')
else
  printf '%s\n' "install.sh: sha256sum or shasum is required" >&2
  exit 1
fi
[ "$actual" = "$expected" ] || { printf 'install.sh: checksum verification failed for %s\n' "$asset" >&2; exit 1; }

mkdir -p "$install_dir"
destination="$install_dir/plugman"
if [ -L "$destination" ] || { [ -e "$destination" ] && [ ! -f "$destination" ]; }; then
  printf 'install.sh: refusing to replace non-regular file: %s\n' "$destination" >&2
  exit 1
fi
staged=$(mktemp "$install_dir/.plugman.install.XXXXXX")
cp "$temporary_dir/$asset" "$staged"
chmod 755 "$staged"
mv -f "$staged" "$destination"
staged=""

path_updated=false
profile=""
case ":${PATH}:" in
  *":${install_dir}:"*) ;;
  *)
    if [ "$install_dir" = "${HOME}/.local/bin" ] && [ "${PLUGMAN_NO_MODIFY_PATH:-}" != "1" ]; then
      case "${SHELL:-}" in
        */zsh) profile="${ZDOTDIR:-${HOME}}/.zshrc" ;;
        */bash)
          if [ "$os" = "darwin" ]; then profile="${HOME}/.bash_profile"; else profile="${HOME}/.bashrc"; fi
          ;;
        *) profile="" ;;
      esac
      marker="# Added by Plugman installer"
      if [ -n "$profile" ] && ! grep -F "$marker" "$profile" >/dev/null 2>&1; then
        printf '\n%s\n%s\n' "$marker" 'export PATH="$HOME/.local/bin:$PATH"' >> "$profile"
      fi
      if [ -n "$profile" ]; then path_updated=true; fi
    fi
    ;;
esac

printf 'Installed Plugman %s to %s\n' "$version" "$install_dir/plugman"
if [ "$path_updated" = true ]; then
  printf 'Added Plugman to PATH in %s. Restart your shell, then run it from any Obsidian vault root.\n' "$profile"
else
  printf 'Add %s to PATH, then run plugman from any Obsidian vault root.\n' "$install_dir"
fi
