# 8. Vim's keys, and a layout every terminal draws the same

**Status:** Accepted  
**Date:** 2026-11-07

## Context

narada's keys had grown one letter at a time to about 130, each a single key whose meaning changed with where you were: `e` archived, reacted or answered an agent; `d` trashed or deleted; `z` undid; `u` switched accounts; `n` renamed. Nothing told you which key did what until you opened the key sheet, and every new feature fought for a letter. The user lives in Neovim with Space as the leader, and wants every tool that has Vim-like keys to follow Vim, with Space as the leader everywhere.

The screen drew everything in boxes: each list row a card, each message a bubble, each mail a framed card. In kitty under tmux a row holding Hindi or Marathi broke these. kitty puts a whole syllable (का, स्क) in one cell, tmux gave its vowel sign and each joined consonant cells of their own, so everything after such text on a row was drawn a few columns left of where narada (and tmux) put it. That pushed a bubble's border out of line and left stray `│` characters behind, in the list and in the conversation on the same row. Bubble Tea's renderer also cuts any row wider than the screen by its own count, which is wider than the terminal's for these scripts.

## Decision

- **Keys work like Vim's.** NORMAL mode moves and acts; `i` writes (INSERT, `Esc` back); `v` picks a range (VISUAL); `:` runs a named command; keys can come in twos (`gg`, `dd`, `yy`, `za`, `zq`, `gf`, `gx`, `gt`). **Space is the leader**: a menu of everything else shows after 350 ms, and never flashes when the next key follows at once. `/` finds anything, narada's own actions among the results with their keys, so the screen teaches its keys. One table (keys.go) feeds `?`, `/`, the README and Vayu's keybinding search.
- **The keys translate to actions** (vimKey → actKey), so each action keeps one implementation whatever key reaches it, and `/` and `:` replay keys rather than call actions of their own.
- **Deleting a WhatsApp message for everyone still asks**: `dd` asks, `dd` again deletes, because that can't be undone (`u` undoes archive, trash, read and star).
- **Nothing is drawn to the right of text that a terminal might measure differently.** Lists are rows with a bar on the left; messages hang off a bar in the sender's colour; places and sections are a top bar and a strip. Frames remain where they hold text you typed or narada wrote (the message box, popups), padded by the terminal's count.
- **Every row is laid out to the terminal's count, not the library's.** narada splits joined consonants with a ZWNJ when it draws them (only on screen; copies and sends keep the text), pads each row to its wcwidth, leaves the last column free, and tells the renderer the screen is 4096 wide so it never cuts a row. Vowel signs are tmux's part: Vayu's tmux.conf gives them no cell (`codepoint-widths`).
- **Colour means something and comes from varna's whole palette**: each place its own (Mail blue, Chat teal, WhatsApp green, AI requests peach), each person their own, mentions pink, files yellow, links sky, yours mauve, new red, always with a shape or word beside it.

## Consequences

- Every key changed meaning or moved; the old ones are gone rather than aliased where Vim gives them another meaning (`z` is a prefix now, `u` is undo). The changelog lists the moves.
- Joined consonants show with a halant between them (स्‌क) rather than as one shape: the price of a row that stays in line. A terminal that counts like kitty without tmux needs no ZWNJ, but narada can't tell which it is drawing for.
- Other tools in tmux still slide on joined consonants; only vowel signs are fixed for them.
- The leader menu and `:` commands are new surfaces to keep in sync with the key table; tests check the README has every key and the keybinding file is current.
