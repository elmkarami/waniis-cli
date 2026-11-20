#!/bin/sh
# Generates shell completions into ./completions for the release archives and the Homebrew formula.
set -eu
mkdir -p completions
for sh in bash zsh fish powershell; do
  go run ./cmd/waniis completion "$sh" > "completions/waniis.$sh"
done
