#!/bin/sh
# SnapSloth installer for Linux and macOS:
#   curl -fsSL https://raw.githubusercontent.com/vimla01/snapsloth/main/install.sh | sh
# Installs Ollama (if missing), pulls the model, and puts `snapsloth` in ~/.local/bin.
# Set SNAPSLOTH_MODEL=gemma4:e2b for the smaller, faster model.
set -eu

REPO="vimla01/snapsloth"
MODEL="${SNAPSLOTH_MODEL:-gemma4:e4b}"
BIN_DIR="${SNAPSLOTH_BIN_DIR:-$HOME/.local/bin}"

say() { printf '\033[1;32m🦥 %s\033[0m\n' "$*"; }
die() { printf '\033[1;31m✗ %s\033[0m\n' "$*" >&2; exit 1; }

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in linux | darwin) ;; *) die "unsupported OS: $os (on Windows, use install.ps1)" ;; esac
case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) die "unsupported CPU: $(uname -m)" ;;
esac

# 1. Ollama, the local model runner.
if command -v ollama >/dev/null 2>&1; then
	say "Ollama is already installed"
else
	say "Installing Ollama..."
	if [ "$os" = linux ]; then
		curl -fsSL https://ollama.com/install.sh | sh
	elif command -v brew >/dev/null 2>&1; then
		brew install ollama
		brew services start ollama
	else
		die "Install Ollama from https://ollama.com/download/mac, open it once, then run this again."
	fi
fi

# Wait for the Ollama server; start it ourselves if nothing else does.
if ! curl -fsS http://localhost:11434/api/version >/dev/null 2>&1; then
	(ollama serve >/dev/null 2>&1 &)
	i=0
	until curl -fsS http://localhost:11434/api/version >/dev/null 2>&1; do
		i=$((i + 1))
		[ "$i" -gt 30 ] && die "Ollama didn't start. Try running 'ollama serve' in another terminal."
		sleep 1
	done
fi

# 2. The model (a few GB, only the first time).
say "Downloading the model $MODEL (a few GB the first time, then it's offline)..."
ollama pull "$MODEL"

# 3. SnapSloth itself: a prebuilt binary, or build it if Go is around.
mkdir -p "$BIN_DIR"
url="https://github.com/$REPO/releases/latest/download/snapsloth_${os}_${arch}"
say "Installing snapsloth to $BIN_DIR..."
if curl -fsL -o "$BIN_DIR/snapsloth.tmp" "$url"; then
	chmod +x "$BIN_DIR/snapsloth.tmp"
	mv "$BIN_DIR/snapsloth.tmp" "$BIN_DIR/snapsloth"
elif command -v go >/dev/null 2>&1; then
	rm -f "$BIN_DIR/snapsloth.tmp"
	say "No prebuilt download for ${os}_${arch}, building it with Go..."
	GOBIN="$BIN_DIR" go install "github.com/$REPO@latest"
else
	rm -f "$BIN_DIR/snapsloth.tmp"
	die "Couldn't download snapsloth and Go isn't installed. Install Go from https://go.dev/dl and run this again."
fi

case ":$PATH:" in
*":$BIN_DIR:"*) run="snapsloth" ;;
*)
	run="$BIN_DIR/snapsloth"
	say "Tip: add $BIN_DIR to your PATH to just type 'snapsloth'."
	;;
esac

say "All set! Start the app with:"
printf '\n    %s ui\n\n' "$run"
