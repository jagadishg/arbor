#!/usr/bin/env bash
# Shared helpers for Arbor's demo recordings: a typewriter effect and colored
# narration so the asciinema casts read like a guided walkthrough.

PROMPT=$'\033[38;5;213m❯\033[0m '
CYAN=$'\033[38;5;44m'
DIM=$'\033[38;5;245m'
RESET=$'\033[0m'

# type_run "<command>" [pause-seconds] — print a prompt, type the command out
# character by character, run it in the current shell, then pause.
type_run() {
  local cmd="$1" pause="${2:-1.2}" i
  printf '%s' "$PROMPT"
  for (( i=0; i<${#cmd}; i++ )); do
    printf '%s' "${cmd:$i:1}"
    sleep 0.018
  done
  printf '\n'
  eval "$cmd"
  sleep "$pause"
}

# comment "<text>" — a dimmed narration line between steps.
comment() {
  printf '\n%s# %s%s\n' "$DIM" "$1" "$RESET"
  sleep 0.6
}

# title "<text>" — a bright banner shown once at the top.
title() {
  printf '%s%s%s\n\n' "$CYAN" "$1" "$RESET"
  sleep 0.8
}
