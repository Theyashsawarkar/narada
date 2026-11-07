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
	// Run is what / plays to do it when picked from the results ("" when
	// it can't be done from there: it needs a place, or is typed)
	Run string
}

// The keys work like Vim's: NORMAL moves and acts, i writes (INSERT), v
// picks a range (VISUAL), : takes a command, and Space is the leader, its
// menu (the Space group) showing after a moment.
var Keys = []Key{
	{"Move", "j / k", "Move to the next / previous thread or chat (j at the end loads older mail)", ""},
	{"Move", "gg / G", "Jump to the first / last thread, chat or message", "gg"},
	{"Move", "Ctrl+d / Ctrl+u", "Scroll what's open half a page down / up", ""},
	{"Move", "J / K", "Scroll what's open a line down / up", ""},
	{"Move", "h / l", "Move to the list / to what's open", ""},
	{"Move", "Enter", "Open the thread or chat (marks it read)", ""},
	{"Move", "q / Esc", "Go back, then quit", ""},
	{"Move", "[ / ]", "Switch to the previous / next section (Unread, Groups, Sent…)", "]"},
	{"Move", "1 / 2 / 3 / 4", "Go to Mail / Chat / WhatsApp / the AI requests", ""},
	{"Move", "gt / gT", "Go to the next / previous place", "gt"},
	{"Move", "Tab / Shift+Tab", "Go to the next / previous of Mail, Chat and WhatsApp", ""},
	{"Move", "Ctrl+n / Ctrl+p", "Switch to the next / previous chat, staying in the conversation", ""},
	{"Move", "n / N", "Jump to the next / previous unread thread or chat", "n"},
	{"Move", "/", "Search everything: actions, chats, people, messages and mail (Gmail's search words work)", ""},
	{"Move", "+ / -", "Set how many older mails j at the end loads (10, 25, 50, 100)", "+"},
	{"Move", "W", "Save the current search as a section", "W"},
	{"Move", "X", "Remove the current saved-search section", "X"},
	{"Act", "i / a", "Start writing a message in the chat (a in mail: reply to everyone)", ""},
	{"Act", "r", "Send a reply (in chat: to the picked message, quoting it)", "r"},
	{"Act", "R", "Send a reply to everyone on the thread (reply all; WhatsApp group: privately)", "R"},
	{"Act", "f", "Send the thread or the picked WhatsApp message on to someone (forward)", "f"},
	{"Act", "c", "Create a new mail, or a message in this chat (compose)", "c"},
	{"Act", "dd", "Move the thread or WhatsApp chat out of the inbox (archive; picking: delete yours, dd twice)", "dd"},
	{"Act", "#", "Move the thread to the trash", "#"},
	{"Act", "u", "Undo the last change, or cancel a send in its undo window", "u"},
	{"Act", "m / M", "Set it as read / unread (or everything picked)", "m"},
	{"Act", "*", "Toggle the star on the thread or picked message", "*"},
	{"Act", "L", "Add or remove a label", "L"},
	{"Act", "v", "Pick several (VISUAL): j/k grow it, then dd, m, M, * or y act on all", "v"},
	{"Act", "V", "Pick the whole list (again clears)", "V"},
	{"Act", "Alt+r", "Set everything in this list as read", ""},
	{"Act", "yy / Y", "Copy the picked message / the link to the thread or chat", "Y"},
	{"Act", "gf / gF", "Open / save the attachments (kept in ~/Downloads/narada)", "gf"},
	{"Act", "gx / o", "Open a link from what's open, by its number", "o"},
	{"Act", "za", "Toggle every message in the thread open / folded", "za"},
	{"Act", "zq", "Toggle the quoted text in the thread", "zq"},
	{"Act", "b", "Open the thread or chat in the browser", "b"},
	{"Act", "U", "Send a file into this chat", "U"},
	{"Act", "t", "Move a scheduled send to another time (in Scheduled)", "t"},
	{"Act", "p", "Toggle the chat pinned on top (WhatsApp: on your phone too)", "p"},
	{"Act", "Enter (picking)", "Jump to the message a reply quotes; on a photo, view it full size", ""},
	{"Act", "Backspace (picking)", "Jump back to the reply you came from", ""},
	{"Act", "e (picking)", "Toggle a reaction on the picked message, or send one to a status (to whoever posted it)", ""},
	{"Act", "r (picking a status)", "Send a reply to a WhatsApp status (in your chat with whoever posted it, quoting it)", ""},
	{"Act", "E (picking)", "Edit your picked message (WhatsApp: within 15 minutes)", ""},
	{"Act", "P (picking a poll)", "Set your vote in the picked WhatsApp poll", ""},
	{"Act", "Z (WhatsApp)", "Set the chat muted for 8 hours (again: a week, then always, then unmuted)", "Z"},
	{"Act", "X (WhatsApp)", "Set the chat's messages to disappear after 24 hours (again: 7 days, 90 days, off)", ""},
	{"Act", "I (WhatsApp group)", "Show the group's people, admins and description", "I"},
	{"Act", "B (WhatsApp)", "Set the person in this chat as blocked (B twice; again unblocks)", ""},
	{"Act", "L (WhatsApp, not linked)", "Connect this machine to WhatsApp with a QR code (link)", ""},
	{"Act", "P (WhatsApp, not linked)", "Connect this machine to WhatsApp with your phone number and a code (link)", ""},
	{"Space", "Space m / c / w / a", "Go to Mail / Chat / WhatsApp / the AI requests", "Space m"},
	{"Space", "Space f", "Search everything (the same as /)", "Space f"},
	{"Space", "Space /", "Search only here: this section or this chat", "Space /"},
	{"Space", "Space n", "Create a new mail or message", "Space n"},
	{"Space", "Space N", "Start a conversation with someone", "Space N"},
	{"Space", "Space S", "Toggle the star", "Space S"},
	{"Space", "Space l", "Add or remove a label", "Space l"},
	{"Space", "Space u", "Show only the unread", "Space u"},
	{"Space", "Space o", "Open a link", "Space o"},
	{"Space", "Space v / V", "Open / save the attachments", "Space v"},
	{"Space", "Space U", "Send a file into this chat", "Space U"},
	{"Space", "Space R", "Rename the chat (spaces and groups for everyone)", "Space R"},
	{"Space", "Space p", "Toggle the chat pinned", "Space p"},
	{"Space", "Space x", "Switch account (all, then each)", "Space x"},
	{"Space", "Space r", "Reload mail and chat from Google now (sync)", "Space r"},
	{"Space", "Space b / y", "Open in the browser / copy the link", "Space b"},
	{"Space", "Space Space", "Open the next unread", "Space Space"},
	{"Space", "Space ?", "Show every key", "Space ?"},
	{"Space", "Space q", "Quit narada", ""},
	{"Claude", "Space k / @", "Start a question to Claude, with this thread or chat as context", "@"},
	{"Claude", "Space s", "Show Claude's summary of the thread or the chat's recent messages", "S"},
	{"Claude", "Space d", "Create a reply draft with Claude, then edit it in your editor", "D"},
	{"Claude", "Space t", "Show today's digest: what came in and what needs you", "T"},
	{"Other", ":", "Run a command: archive, trash, read, unread, star, label, later, mute, rename, account, sync, q (Tab completes)", ""},
	{"Other", "?", "Show every key", ""},
	{"Insert", "Esc", "Go back to NORMAL mode (what you wrote stays)", ""},
	{"Insert", "Enter", "Send the message and keep writing the next one", ""},
	{"Insert", "Alt+Enter / Ctrl+j", "Start a new line in the message", ""},
	{"Insert", "Ctrl+o", "Add a file to the message", ""},
	{"Insert", "Ctrl+v", "Paste a copied image (a screenshot) into the message", ""},
	{"Insert", "Ctrl+t", "Send the message later (tomorrow 9am, fri 6pm, in 2h)", ""},
	{"Insert", "Ctrl+r (WhatsApp)", "Record a voice note (again stops and adds it; Enter sends it, Esc drops it)", ""},
	{"Insert", "Ctrl+p (WhatsApp)", "Send what you wrote as a poll: the question, then one option per line", ""},
	{"Insert", "@ (WhatsApp)", "Pick someone to tag from the list (Up/Down, Enter or Tab; Esc for none)", ""},
	{"Insert", "←→ Alt+b/f Ctrl+a/e", "Move the cursor by letter, word, line start / end", ""},
	{"Insert", "Ctrl+w / Ctrl+k / Ctrl+u", "Delete the word before the cursor / to the end / the whole message", ""},
	{"Compose", "Tab / Shift+Tab", "Move to the next / previous field (Tab adds what you typed first)", ""},
	{"Compose", "Up / Down", "Pick a contact from the suggestions", ""},
	{"Compose", "Enter / ,", "Add the picked contact or typed address, and stay for more", ""},
	{"Compose", "Backspace", "Remove the last recipient (in an empty To, Cc or Bcc)", ""},
	{"Compose", "Ctrl+s", "Send the mail (after the undo window)", ""},
	{"Compose", "Ctrl+t", "Send the mail later (pick or type when: tomorrow 9am, fri 6pm, in 2h)", ""},
	{"Compose", "Ctrl+d", "Save the mail as a Gmail draft and close it", ""},
	{"Compose", "Ctrl+o", "Add files to the mail (x removes one in Files)", ""},
	{"Compose", "Ctrl+e", "Edit the whole mail in your editor, then come back", ""},
	{"Compose", "Ctrl+q", "Toggle the quoted text under a reply or forward", ""},
	{"Compose", "Left / Right", "Select a recipient pill (in an empty To, Cc or Bcc; Backspace removes it)", ""},
	{"Compose", "Ctrl+b", "Toggle the Cc and Bcc fields", ""},
	{"Compose", "Ctrl+g", "Toggle your Gmail signature under the mail", ""},
	{"Compose", "Ctrl+f", "Switch the account the mail is sent from", ""},
	{"Compose", "PgUp / PgDn", "Scroll the message you're replying to", ""},
	{"Compose", "Esc", "Cancel the mail (twice once something is written)", ""},
	{"Search", "Up / Down", "Move between the results (Ctrl+p / Ctrl+n too)", ""},
	{"Search", "Enter", "Open the result, or do the action", ""},
	{"Search", "Tab", "Switch where it looks: here, or everywhere", ""},
	{"Search", "Ctrl+u", "Clear the search", ""},
	{"Search", "Esc", "Close the search", ""},
	{"Review", "y", "Send the message you wrote (after the undo window)", ""},
	{"Review", "t", "Send the message later (pick or type when)", ""},
	{"Review", "e", "Edit the message again in your editor", ""},
	{"Review", "+", "Add a file to the message", ""},
	{"Review", "-", "Remove the last file added to the message", ""},
	{"Review", "d", "Save the message as a Gmail draft instead of sending", ""},
	{"Review", "n", "Delete the message you wrote (discard)", ""},
	{"Approve", "y", "Send the message an agent wrote (after the undo window)", ""},
	{"Approve", "e", "Edit the agent's message in your editor first", ""},
	{"Approve", "u", "Undo your edit of the agent's message", ""},
	{"Approve", "d", "Toggle the changes marked in your edit of the agent's message", ""},
	{"Approve", "s", "Move to the next message waiting (leaves this one waiting)", ""},
	{"Approve", "J / K", "Scroll the details of the agent's message", ""},
	{"Approve", "n", "Cancel the agent's message (decline; nothing is sent)", ""},
	{"Approve", "q", "Leave the agent's message waiting and go back", ""},
	{"Setup", "Tab", "Switch to the next path completion (Enter on a folder opens it)", ""},
	{"Setup", "Enter", "Save the OAuth client file at this path, or start the Google sign-in", ""},
	{"Setup", "Ctrl+t", "Enter the OAuth client ID and secret by hand instead", ""},
	{"Setup", "Ctrl+o", "Open the Google Cloud console credentials page", ""},
	{"Setup", "Ctrl+g", "Show the steps for creating the OAuth client", ""},
	{"Setup", "Ctrl+d", "Start the demo on example mail instead", ""},
	{"Setup", "n", "Rename the account before signing in", ""},
	{"Setup", "c", "Pick a different OAuth client file", ""},
	{"Setup", "y", "Copy the sign-in link", ""},
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
		case "Insert":
			scope = "narada writing"
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
