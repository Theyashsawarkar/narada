#!/usr/bin/env bash
# narada as a tmux plugin: TPM entry point (also works with a plain
# `run-shell` on this file). Binds prefix + @narada-key to open narada in
# a popup, and fills #{narada_status} in your status line with the unread
# counts.
#
# Options (set them before TPM's `run` line):
#   @narada-key       key after the prefix (default e; "none" binds nothing)
#   @narada-width     popup width  (default 96%)
#   @narada-height    popup height (default 94%)
#   @narada-title     popup title  (default " narada ")
#   @narada-launcher  your own popup command instead of display-popup: it
#                      is run with display-popup's arguments appended
#                      (-E -d DIR -T TITLE CMD), tmux formats expanded, e.g.
#                      "~/.tmux/scripts/popup.sh '#{client_name}'"
#
# Status line: put #{narada_status} in status-right (or status-left); it
# becomes "󰇮 3  󰭹 2  󰖣 1" (unread mail threads, chat spaces, WhatsApp
# chats), empty when there is nothing new.

dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
run="$dir/scripts/narada-run"

opt() { local v; v=$(tmux show-option -gqv "$1"); printf '%s' "${v:-$2}"; }
key=$(opt @narada-key e)
width=$(opt @narada-width 96%)
height=$(opt @narada-height 94%)
title=$(opt @narada-title ' narada ')
launcher=$(opt @narada-launcher '')

if [ "$key" != none ]; then
  cmd="'$run' --popup"
  if [ -n "$launcher" ]; then
    open="run -b \"$launcher -E -d '#{pane_current_path}' -T '$title' $cmd\""
  else
    open="display-popup -E -w '$width' -h '$height' -d '#{pane_current_path}' -T '$title' \"$cmd\""
  fi
  tmux bind-key -N "Open narada mail and chat popup (Gmail, Google Chat, WhatsApp)" "$key" "$open"
fi

# #{narada_status} -> #('.../narada-run' status --tmux)
for side in status-right status-left; do
  value=$(tmux show-option -gqv "$side")
  case $value in
    *'#{narada_status}'*)
      tmux set-option -gq "$side" "${value//\#\{narada_status\}/#($run status --tmux)}" ;;
  esac
done
