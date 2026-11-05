package tui

import (
	"fmt"
	"strings"
)

// Key is one row of the key sheet. The same table feeds the ? sheet, the
// README and scripts/keybindings (Vayu's keybinding search reads that
// file; a test keeps it in sync).
type Key struct {
	Group string
	Keys  string
	Desc  string
}

var Keys = []Key{
	{"Move", "j / k", "Move to the next / previous thread or space (j at the end loads older mail)"},
	{"Move", "+ / -", "Set how many older mails j at the end loads (10, 25, 50, 100)"},
	{"Move", "g / G", "Jump to the first / last"},
	{"Move", "h / l", "Switch to the previous / next section"},
	{"Move", "Tab / Shift+Tab", "Switch to the next / previous of mail, chat and WhatsApp"},
	{"Move", "Alt+m / Alt+c / Alt+w", "Switch straight to mail / chat / WhatsApp"},
	{"Move", "1 / 2", "Focus the list / the preview"},
	{"Move", "Enter", "Open the thread or space (marks it read)"},
	{"Move", "J / K", "Scroll the preview down / up"},
	{"Move", "E", "Toggle every message in the thread open / folded"},
	{"Move", "Q", "Toggle the quoted text in the thread"},
	{"Move", "o", "Open a link from the preview by its number"},
	{"Move", "Ctrl+d / Ctrl+u", "Scroll the preview half a page down / up"},
	{"Move", "/ or s", "Open the search window (searches where you are: this section, or this chat)"},
	{"Move", "W", "Save the current search as a section"},
	{"Move", "X", "Remove the current saved-search section"},
	{"Move", "u", "Switch account (all, then each one)"},
	{"Act", "c", "Create a new mail, or a new message in this space (compose)"},
	{"Act", "r", "Send a reply to the thread (in chat: to the picked message, quoting it)"},
	{"Act", "a", "Send a reply to everyone on the thread (reply all)"},
	{"Act", "f", "Send the thread on to someone else (forward)"},
	{"Act", "e", "Move the thread out of the inbox (archive; or every selected one)"},
	{"Act", "d", "Move the thread to the trash (or every selected one)"},
	{"Act", "m", "Set the thread or chat as read (mark read; or every selected one)"},
	{"Act", "M", "Set the thread or chat as unread (mark unread; or every selected one)"},
	{"Act", "*", "Toggle the star on the thread"},
	{"Act", "L", "Add or remove a label (fzf picker)"},
	{"Act", "x", "Select the thread or chat for e / d / m / M (pick several)"},
	{"Act", "Ctrl+x", "Select every thread or chat in the list (again clears)"},
	{"Act", "Alt+r", "Set everything in this list as read (mark all read)"},
	{"Act", "i", "Start typing a chat message right in the preview (Ctrl+t sends it later)"},
	{"Act", "Enter / 2 (chat)", "Pick messages in the chat with j/k (k past the first loads earlier ones)"},
	{"Act", "Enter (picking)", "Jump to the message a reply quotes (again goes further back); on a photo, view it full size"},
	{"Act", "Space (picking)", "Open the picked photo or GIF full size (h/l previous / next, v opens it, Esc back)"},
	{"Act", "Backspace (picking)", "Jump back to the reply you came from"},
	{"Act", "e (picking)", "Toggle a reaction on the picked message (yours are marked ✓; picking one removes it)"},
	{"Act", "E (picking)", "Edit your picked message in the message box (WhatsApp: within 15 minutes)"},
	{"Act", "d d (picking)", "Delete your picked message, for everyone (WhatsApp: someone else's is deleted for you, here only)"},
	{"Act", "f (picking, WhatsApp)", "Forward the picked message to another chat (type a contact, group or number)"},
	{"Act", "P (picking, WhatsApp poll)", "Vote in the picked poll (type the option numbers; empty takes your vote back)"},
	{"Act", "R (picking, WhatsApp group)", "Send a private reply to the picked group message (in your chat with its sender, quoting it)"},
	{"Act", "p", "Toggle the chat pinned on top of the list (WhatsApp: on your phone too, three at most)"},
	{"Act", "e (WhatsApp)", "Move the chat into Archived, or back out of it (archive; your phone follows)"},
	{"Act", "Z (WhatsApp)", "Mute the chat for 8 hours (again: a week, then always, then unmuted)"},
	{"Act", "I (WhatsApp group)", "Show the group's people, admins and description"},
	{"Act", "B (WhatsApp)", "Block the person in this chat (B twice; again unblocks)"},
	{"Act", "* (picking, WhatsApp)", "Toggle a star on the picked message (your phone follows)"},
	{"Act", "[ / ]", "Switch to the previous / next chat (staying in the conversation)"},
	{"Act", "Ctrl+k", "Open the chat switcher (search chats, people and messages)"},
	{"Act", "Ctrl+o (typing)", "Add a file to the chat message you're typing"},
	{"Act", "Ctrl+v (typing)", "Paste a copied image (a screenshot) into the chat message as a file"},
	{"Act", "Ctrl+r (typing, WhatsApp)", "Record a voice note for the message (again stops and adds it; Enter sends it, Esc drops it)"},
	{"Act", "Ctrl+p (typing, WhatsApp)", "Send what you typed as a poll: the question, then one option per line (end the question with (several) to allow several)"},
	{"Act", "Enter (typing)", "Send the chat message and keep typing the next one (Esc leaves the box)"},
	{"Act", "Alt+Enter / Ctrl+j (typing)", "Start a new line in the chat message"},
	{"Act", "@ (typing, WhatsApp)", "Pick someone to tag from the list (Up/Down, Enter or Tab; Esc for none)"},
	{"Act", "←→ Alt+b/f Ctrl+a/e (typing)", "Move the cursor in the message by letter, word, line start / end"},
	{"Act", "↑↓ (typing)", "Move the cursor up / down a line in a long message"},
	{"Act", "Ctrl+w / Ctrl+k / Ctrl+u (typing)", "Delete the word before the cursor / to the end / the whole message"},
	{"Act", "L (WhatsApp, not linked)", "Add this machine to WhatsApp (link it) with a QR code shown in the tab"},
	{"Act", "P (WhatsApp, not linked)", "Add this machine to WhatsApp (link it) with your phone number and a code"},
	{"Act", "t", "Move a scheduled send to another time (in Scheduled; Enter edits it)"},
	{"Act", "n", "Rename the chat (spaces and groups for everyone, on Google Chat and WhatsApp; a DM only here)"},
	{"Act", "N", "Start a conversation (opens the one you have with them, earlier messages and all)"},
	{"Act", "v", "Open the attachments in their apps (kept in ~/Downloads/narada; picker when several)"},
	{"Act", "V", "Save the attachments to ~/Downloads/narada without opening them"},
	{"Act", "U", "Send a file into this chat (Google Chat or WhatsApp; file picker with preview)"},
	{"Act", "z", "Cancel the last change, or a send in its undo window (undo)"},
	{"Act", "A", "Go to the AI requests waiting for your approval"},
	{"Act", "R", "Reload mail and chat from Google now (sync)"},
	{"Act", "b", "Open the thread or space in the browser"},
	{"Act", "y", "Copy the link to the thread or space"},
	{"Claude", "@", "Start a question to Claude, with this thread or space as context (ask)"},
	{"Claude", "S", "Show Claude's summary of the thread or the space's recent messages"},
	{"Claude", "D", "Create a reply draft with Claude, then edit it in your editor"},
	{"Claude", "T", "Show today's digest: what came in and what needs you"},
	{"Other", "?", "Show every key"},
	{"Other", "q / Esc", "Go back, then quit"},
	{"Compose", "Tab / Shift+Tab", "Move to the next / previous field (Tab adds what you typed first)"},
	{"Compose", "Up / Down", "Pick a contact from the suggestions"},
	{"Compose", "Enter / ,", "Add the picked contact or typed address, and stay for more"},
	{"Compose", "Backspace", "Remove the last recipient (in an empty To, Cc or Bcc)"},
	{"Compose", "Ctrl+s", "Send the mail (after the undo window)"},
	{"Compose", "Ctrl+t", "Send the mail later (pick or type when: tomorrow 9am, fri 6pm, in 2h)"},
	{"Compose", "Ctrl+d", "Save the mail as a Gmail draft and close it"},
	{"Compose", "Ctrl+o", "Add files to the mail (file picker with preview; x removes one in Files)"},
	{"Compose", "Ctrl+e", "Edit the whole mail in your editor, then come back"},
	{"Compose", "Ctrl+q", "Toggle the quoted text under a reply or forward"},
	{"Compose", "Left / Right", "Select a recipient pill (in an empty To, Cc or Bcc; Backspace removes it)"},
	{"Compose", "Ctrl+b", "Toggle the Cc and Bcc fields"},
	{"Compose", "Ctrl+g", "Toggle your Gmail signature under the mail"},
	{"Compose", "Ctrl+f", "Switch the account the mail is sent from"},
	{"Compose", "PgUp / PgDn", "Scroll the message you're replying to"},
	{"Compose", "Esc", "Cancel the mail (twice once something is written)"},
	{"Search", "Up / Down", "Move between the results (Ctrl+p / Ctrl+n too)"},
	{"Search", "Enter", "Open the result (mail results stay listed as a search section)"},
	{"Search", "Tab", "Switch where it looks: this section or all mail, this chat or every chat"},
	{"Search", "Ctrl+u", "Clear the search"},
	{"Search", "Esc", "Close the search window"},
	{"Review", "y", "Send the message you wrote (after the undo window)"},
	{"Review", "t", "Send the message later (pick or type when)"},
	{"Review", "e", "Edit the message again in your editor"},
	{"Review", "+", "Add a file to the message (attach; file picker with preview)"},
	{"Review", "-", "Remove the last file added to the message"},
	{"Review", "d", "Save the message as a Gmail draft instead of sending"},
	{"Review", "n", "Delete the message you wrote (discard)"},
	{"Approve", "y", "Send the message an agent wrote (after the undo window)"},
	{"Approve", "e", "Edit the agent's message in your editor first"},
	{"Approve", "u", "Undo your edit of the agent's message"},
	{"Approve", "d", "Toggle the changes marked in your edit of the agent's message"},
	{"Approve", "s", "Move to the next message waiting (leaves this one waiting)"},
	{"Approve", "J / K", "Scroll the details of the agent's message"},
	{"Approve", "n", "Cancel the agent's message (decline; nothing is sent)"},
	{"Approve", "q", "Leave the agent's message waiting and go back"},
	{"Setup", "Tab", "Switch to the next path completion (Enter on a folder opens it)"},
	{"Setup", "Enter", "Save the OAuth client file at this path, or start the Google sign-in"},
	{"Setup", "Ctrl+t", "Enter the OAuth client ID and secret by hand instead"},
	{"Setup", "Ctrl+o", "Open the Google Cloud console credentials page"},
	{"Setup", "Ctrl+g", "Show the steps for creating the OAuth client"},
	{"Setup", "Ctrl+d", "Start the demo on example mail instead"},
	{"Setup", "n", "Rename the account before signing in"},
	{"Setup", "c", "Pick a different OAuth client file"},
	{"Setup", "y", "Copy the sign-in link"},
}

// KeybindLines renders the table as Vayu `# keybind:` annotations.
func KeybindLines() string {
	var b strings.Builder
	b.WriteString("# Keys inside the narada screen, for Vayu's keybinding search\n")
	b.WriteString("# (keybind-search.py reads ~/.tmux/plugins/*/scripts). Generated from\n")
	b.WriteString("# internal/tui/keys.go by `go test ./internal/tui -update`; don't edit.\n")
	for _, k := range Keys {
		scope := "narada"
		switch k.Group {
		case "Compose":
			scope = "narada compose"
		case "Search":
			scope = "narada search"
		case "Review":
			scope = "narada review"
		case "Approve":
			scope = "narada approve"
		case "Setup":
			scope = "narada setup"
		}
		fmt.Fprintf(&b, "# keybind: Tmux/%s | %s | %s\n", scope, k.Keys, k.Desc)
	}
	return b.String()
}
