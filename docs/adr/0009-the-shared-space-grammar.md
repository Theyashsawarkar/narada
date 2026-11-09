# 9. The shared Space grammar, keys as data

**Status:** Accepted (supersedes ADR 8's single-letter actions, digits, `J`/`K` and the leader letters)  
**Date:** 2026-11-09

## Context

ADR 8 made narada's keys Vim-like, but on top of the old single letters: `vimKey` translated new keys into old action names, and any key it didn't know fell through to the old actions, so undocumented keys acted (`e` archived at once; `x`, `s`, `Q`, `D`/`S`/`T`, `Ctrl+x` did things no sheet listed). The leader's letters were narada's own (`Space m` Mail, `Space d` Claude, `Space Space` next unread), digits were places, and nothing counted.

Meanwhile every other Vayu tool moved to one key grammar ([vyākaraṇa](https://github.com/Theyashsawarkar/vyakarana)): Vim's vocabulary as bare keys, and everything else under Space in groups whose letters mean the same in every tool. The user wants narada to match, with three choices of their own: `e` and `Space m a` both archive (`u` undoes it, so one key is allowed); `Ctrl+j`/`Ctrl+k` scroll what's open 3 lines (`scroll_lines`); forward and react live only under `Space i`.

## Decision

- **Keys are data.** `keymap.go` lists every key as a binding (keys, action name, verb-first description, where it works, mode); vyākaraṇa's engine turns key presses into an action and a count (`5j`, `3gt`), waits for sequences (`gg`, `Space m s`) for as long as it takes (`Esc` cancels, `Backspace` goes up a level), and `actions.go` runs the action. A key that isn't in the table does nothing (it says so); an old key says where it moved, for one release. The same table makes `?`, the which-key menu, `Space Space` (the palette), `/`'s actions, the README's key tables and `scripts/keybindings`, and a test (`keymaptest.All`) checks verb-first descriptions, the reserved groups, and that no key hides another.
- **The shared letters.** Bare keys are Vim's: `j k h l`, `gg G`, `H L` (places), `]] [[` (sections), `Ctrl+d/u/f/b/j/k/e/y`, `n N` (unread), `J K` (messages), `zz zt zb`, `gd`, `i a`, `o O`, `dd`, `e`, `yy`, `p`, `~`, `u`, `v V`, `:`. `r` stays reply, narada's documented exception. Under Space: `t` tabs, `y` copy, `f` find, `r` refresh, `a` add, `d` delete, `e` edit, `o` open, `g` go to, `m` mark, `k` ask the AI, `,` settings; narada's own groups use free letters: `c` this chat, `i` this message, `w` window, `x` the AI requests. `Space Space` is the palette.
- **Anything you can't undo asks first, and only `y` says yes**: `dd` (trash, delete a message for everyone or for you), removing a saved section, blocking. Discarding what you wrote on the review screen is `n` twice, because `y` there sends.
- **The command bar floats in the middle** (`:`): ghost text, fuzzy suggestions with each command's keys, arguments completed from the data they name (labels, accounts, sections, chats, times, options), counts and VISUAL's range in front, history on disk. Every action is a command by its name, so the bar and the keys can never disagree; `:set` and `:map` change the session, `[keys.normal]` in config.toml keeps your keys.
- **Vim's verbs over the same actions**: `d` with a motion picks the threads it spans and asks once; `.` repeats the last change; `u` / `Ctrl+r` walk a stack of changes and their inverses (a send is never redone: its only undo is the undo window); marks live in the database so they last; a jump list records opens, `gg`, `G` and marks; `Space f h` searches what's open (`n` / `N` step until `Esc`). `?` stays help, as the shared grammar says, rather than Vim's backward search.
- **The message box has its own NORMAL** (Esc while writing; the compose body too): Vim's moves and edits on the text, done by one pure function (`boxApply`) both editors share, so the chat box and the compose body can't drift. Enter still sends a chat message, Ctrl+s a mail; `message_box_vim = false` turns it off.
- **Statuses are never marked seen on WhatsApp**: the app layer drops a status read receipt wherever one could start (queue, flush, the client), so no screen or command can send one.
- **actKey stays as the implementation** of the older actions, called by name from `actions.go`, until each moves over.

## Consequences

- About forty keys moved (the changelog lists them); digits count now, so `3` no longer opens WhatsApp (`L`, `3gt` and `Space t w` do).
- Typed fast or sent by tmux, keys can reach the screen as one message (`dd`); narada splits it, and once a key starts typing (`i`), the rest is text.
- A second Esc is needed to leave a box with text in it (the first goes to its NORMAL); in the compose screen, Esc from the body's NORMAL is the screen's own Esc (once warns, twice discards).
- vyākaraṇa gained `Backspace` up a level and a `NoPrefixClash` that ignores keys a screen only documents; narada builds against it with a `replace` until vyākaraṇa v0.2.0 is released.
