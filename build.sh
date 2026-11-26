#!/usr/bin/env bash
# ARTEX cross-platform release builder.
#
# The default mode builds one target and embeds the already-exported frontend.
# `./build.sh --release` builds and packages all supported desktop/server targets.
#
# Environment variables:
#   ARTEX_TARGET_OS=linux             One target OS in single-target mode.
#   ARTEX_TARGET_ARCH=amd64           One target arch in single-target mode.
#   ARTEX_TARGETS=linux/amd64,...     Comma-separated targets for multi-target mode.
#   ARTEX_BUILD_VERSION=v0.3.3        Version embedded in the binary and archive name.
#   ARTEX_OUTPUT=/path/to/artex       Explicit binary path in single-target mode.
#   ARTEX_OUTPUT_DIR=dist             Directory for default binary paths.
#   ARTEX_PACKAGE=1                   Create a zip archive for each target.
#   ARTEX_PACKAGE_DIR=dist            Directory for release archives.
#   ARTEX_COMPRESS=off                UPX mode: off, auto, or required.
#   ARTEX_UPX_ARGS="--best --lzma"    Arguments passed to UPX.
#   ARTEX_SKIP_FRONTEND=1             Reuse server/webui/dist (for CI artifact builds).
#   ARTEX_SKIP_NPM_CI=1               Skip npm ci while rebuilding the frontend.
#   ARTEX_GOSUMDB=sum.golang.org      Go checksum database.
set -euo pipefail

cd "$(cd "$(dirname "$0")" && pwd)"

info() { printf '\033[36m[*]\033[0m %s\n' "$*"; }
ok() { printf '\033[32m[+]\033[0m %s\n' "$*"; }
warn() { printf '\033[33m[!]\033[0m %s\n' "$*" >&2; }
die() { printf '\033[31m[x]\033[0m %s\n' "$*" >&2; exit 1; }

usage() {
  cat <<'EOF'
Uso:
  ./build.sh                         compila para o sistema e a arquitetura atuais
  ./build.sh --target linux/amd64   compila para um alvo específico
  ./build.sh --release               compila e empacota todos os alvos suportados

Opções:
  --release              compila os alvos amd64/arm64 de Linux, macOS e Windows e gera zip
  --target OS/ARCH       define um único alvo, por exemplo windows/amd64
  --upx                  força a compressão do binário com UPX (pode afetar a compatibilidade em alguns ambientes Linux)
  --no-compress          não usa UPX; usa só o corte e a compressão do linker do Go e compacta em zip
  --help                 exibe a ajuda

A lista de múltiplos alvos pode ser sobrescrita com ARTEX_TARGETS, por exemplo:
  ARTEX_TARGETS=linux/amd64,windows/amd64 ./build.sh --release
EOF
}

RELEASE_TARGETS_DEFAULT="linux/amd64,linux/arm64,darwin/amd64,darwin/arm64,windows/amd64"
ARTEX_RELEASE="${ARTEX_RELEASE:-0}"
ARTEX_COMPRESS="${ARTEX_COMPRESS:-off}"
ARTEX_PACKAGE="${ARTEX_PACKAGE:-0}"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --release)
      ARTEX_RELEASE=1
      ARTEX_PACKAGE=1
      shift
      ;;
    --target)
      [ "$#" -ge 2 ] || die "--target requer o argumento OS/ARCH"
      target_arg="$2"
      case "$target_arg" in
        */*)
          ARTEX_TARGET_OS="${target_arg%%/*}"
          ARTEX_TARGET_ARCH="${target_arg##*/}"
          ARTEX_TARGETS="$target_arg"
          ;;
        *) die "o alvo deve ser OS/ARCH, por exemplo linux/amd64" ;;
      esac
      shift 2
      ;;
    --no-compress)
      ARTEX_COMPRESS=0
      shift
      ;;
    --upx)
      ARTEX_COMPRESS=required
      shift
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *) die "argumento desconhecido: $1 (use --help para ver o uso)" ;;
  esac
done

command -v go >/dev/null 2>&1 || die "Go não detectado (o projeto requer Go 1.26 ou superior)"

ARTEX_GOSUMDB="${ARTEX_GOSUMDB:-sum.golang.org}"
if [ -z "${ARTEX_BUILD_VERSION:-}" ]; then
  if command -v git >/dev/null 2>&1 && git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    ARTEX_BUILD_VERSION="$(git describe --tags --always --dirty)"
  else
    ARTEX_BUILD_VERSION="dev"
  fi
fi
# Release tags are commonly passed as v0.3.3; keep the binary version consistent.
ARTEX_BUILD_VERSION="${ARTEX_BUILD_VERSION#v}"
ARTEX_OUTPUT_DIR="${ARTEX_OUTPUT_DIR:-dist}"
ARTEX_PACKAGE_DIR="${ARTEX_PACKAGE_DIR:-$ARTEX_OUTPUT_DIR}"
ARTEX_UPX_ARGS="${ARTEX_UPX_ARGS:---best --lzma}"

if [ "${ARTEX_RELEASE}" = "1" ]; then
  ARTEX_TARGETS="${ARTEX_TARGETS:-$RELEASE_TARGETS_DEFAULT}"
else
  ARTEX_TARGET_OS="${ARTEX_TARGET_OS:-$(GOSUMDB="$ARTEX_GOSUMDB" go env GOOS)}"
  ARTEX_TARGET_ARCH="${ARTEX_TARGET_ARCH:-$(GOSUMDB="$ARTEX_GOSUMDB" go env GOARCH)}"
  ARTEX_TARGETS="${ARTEX_TARGETS:-${ARTEX_TARGET_OS}/${ARTEX_TARGET_ARCH}}"
fi

if [ "${ARTEX_SKIP_FRONTEND:-0}" = "1" ]; then
  [ -d server/webui/dist ] || die "ARTEX_SKIP_FRONTEND=1, mas server/webui/dist não existe"
else
  command -v npm >/dev/null 2>&1 || die "npm não detectado (o build estático do front-end requer Node.js/npm)"
  command -v rsync >/dev/null 2>&1 || die "rsync não detectado"
  info "compilando os recursos estáticos do front-end"
  if [ "${ARTEX_SKIP_NPM_CI:-0}" != "1" ]; then
    (cd web && npm ci)
  fi
  (cd web && npm run build:static)
  info "sincronizando os recursos do front-end para server/webui/dist"
  mkdir -p server/webui/dist
  rsync -a --delete web/out/ server/webui/dist/
fi

compress_binary() {
  binary="$1"
  goos="$2"
  case "$ARTEX_COMPRESS" in
    0|off|false|none)
      info "pulando UPX: $binary"
      return 0
      ;;
    auto|required|true|1) ;;
    *) die "ARTEX_COMPRESS deve ser off, auto ou required" ;;
  esac

  if ! command -v upx >/dev/null 2>&1; then
    if [ "$ARTEX_COMPRESS" = "required" ]; then
      die "ARTEX_COMPRESS=required, mas upx não foi detectado"
    fi
    warn "upx não detectado, mantendo o resultado da compressão do linker: $binary"
    return 0
  fi

  before=$(wc -c < "$binary" | tr -d ' ')
  upx_args="$ARTEX_UPX_ARGS"
  [ "$goos" = "darwin" ] && upx_args="$upx_args --force-macos"
  # shellcheck disable=SC2086
  if ! upx $upx_args -- "$binary"; then
    if [ "$ARTEX_COMPRESS" = "required" ]; then
      die "falha na compressão UPX: $binary"
    fi
    warn "o UPX não suporta o formato deste alvo, mantendo o binário sem compressão: $binary"
    return 0
  fi
  after=$(wc -c < "$binary" | tr -d ' ')
  ok "compressão UPX concluída: $binary (${before} -> ${after} bytes)"
}

package_binary() {
  binary="$1"
  goos="$2"
  goarch="$3"
  package_name="artex-${ARTEX_BUILD_VERSION}-${goos}-${goarch}"
  package_root="${ARTEX_PACKAGE_DIR}/${package_name}"
  archive="${ARTEX_PACKAGE_DIR}/${package_name}.zip"

  command -v zip >/dev/null 2>&1 || die "o empacotamento requer zip"
  rm -rf "$package_root" "$archive"
  mkdir -p "$package_root"
  cp "$binary" "$package_root/"
  # O script guardião de inicialização é o ponto de entrada oficial: a atualização com um clique na página depende dele para relançar o processo depois que ele sai;
  # se rodar o próprio binário artex diretamente, após a atualização ele nunca mais sobe. Inclui apenas a versão correspondente ao sistema do alvo.
  if [ "$goos" = "windows" ]; then
    cp start.bat "$package_root/"
  else
    cp start.sh "$package_root/"
    chmod +x "$package_root/start.sh"
  fi
  cp -R skills "$package_root/"
  cp config.example.json "$package_root/"
  if [ -f README.md ]; then cp README.md "$package_root/"; fi
  (cd "$ARTEX_PACKAGE_DIR" && zip -q -r -9 "$(basename "$archive")" "$(basename "$package_root")")
  rm -rf "$package_root"
  ok "Pacote da Release: $archive"
}

build_target() {
  target="$1"
  case "$target" in
    */*) ;;
    *) die "alvo inválido: $target (deve ser OS/ARCH)" ;;
  esac
  goos="${target%%/*}"
  goarch="${target##*/}"
  case "$goos" in
    linux|darwin|windows) ;;
    *) die "sistema não suportado: $goos (suportados: linux, darwin, windows)" ;;
  esac

  binary_name="artex"
  [ "$goos" = "windows" ] && binary_name="artex.exe"
  if [ -n "${ARTEX_OUTPUT:-}" ] && [ "$ARTEX_RELEASE" != "1" ]; then
    output="$ARTEX_OUTPUT"
  else
    output="${ARTEX_OUTPUT_DIR}/artex-${goos}-${goarch}/${binary_name}"
  fi
  mkdir -p "$(dirname "$output")"

  info "compilando ${goos}/${goarch}, versão ${ARTEX_BUILD_VERSION}"
  GOSUMDB="$ARTEX_GOSUMDB" \
  CGO_ENABLED=0 \
  GOOS="$goos" \
  GOARCH="$goarch" \
  go build \
    -tags embedui \
    -trimpath \
    -ldflags "-s -w -buildid= -X main.version=${ARTEX_BUILD_VERSION}" \
    -o "$output" \
    ./cmd/artex

  compress_binary "$output" "$goos"
  if command -v file >/dev/null 2>&1; then file "$output"; fi
  if [ "$ARTEX_PACKAGE" = "1" ]; then package_binary "$output" "$goos" "$goarch"; fi
  ok "compilação concluída: $output"
}

write_checksums() {
  [ "$ARTEX_PACKAGE" = "1" ] || return 0
  checksum_file="$ARTEX_PACKAGE_DIR/SHA256SUMS"
  if command -v sha256sum >/dev/null 2>&1; then
    (cd "$ARTEX_PACKAGE_DIR" && for archive in *.zip; do sha256sum "$archive"; done > "$(basename "$checksum_file")")
  elif command -v shasum >/dev/null 2>&1; then
    (cd "$ARTEX_PACKAGE_DIR" && for archive in *.zip; do shasum -a 256 "$archive"; done > "$(basename "$checksum_file")")
  else
    warn "sha256sum ou shasum não detectado, pulando SHA256SUMS"
    return 0
  fi
  ok "arquivo de checksum: $checksum_file"
}

mkdir -p "$ARTEX_OUTPUT_DIR"
if [ "$ARTEX_PACKAGE" = "1" ]; then mkdir -p "$ARTEX_PACKAGE_DIR"; fi

old_ifs="$IFS"
IFS=','
read -r -a targets <<< "$ARTEX_TARGETS"
IFS="$old_ifs"
[ "${#targets[@]}" -gt 0 ] || die "ARTEX_TARGETS não pode ser vazio"
for target in "${targets[@]}"; do
  target="${target//[[:space:]]/}"
  [ -n "$target" ] || continue
  build_target "$target"
done

if [ "$ARTEX_PACKAGE" = "1" ]; then
  write_checksums
  info "Pacotes da Release gerados em: $ARTEX_PACKAGE_DIR"
fi
