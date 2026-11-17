package tui

import (
	"github.com/Theyashsawarkar/vyakarana/keymap"
	vmode "github.com/Theyashsawarkar/vyakarana/mode"
)

// narada's keys, as data (ADR 9): every key in NORMAL is a binding here,
// the engine (vyakarana) turns key presses into an action name with a
// count, and run (actions.go) does it. The same table feeds the key
// sheet (?), the which-key menu after Space, the palette (Space Space),
// the finder's actions, the README's key tables and scripts/keybindings
// (Vayu's keybinding search). A key that isn't in this table does
// nothing.
//
// The grammar is the one every Vayu tool shares: Vim's vocabulary as bare
// keys, and everything else under Space in groups whose letters mean the
// same in every tool (t tabs, u toggles, y copy, f find, r refresh, a add,
// d delete, e edit, o open, g go to, m mark, k ask the AI); narada's own
// groups use the free letters (c this chat, i this message, w window,
// x the AI requests). r is reply, narada's documented exception.

// views: where a key works, as the engine is told (where()).
const (
	inMail = "mail"
	inChat = "chat" // Google Chat
	inWA   = "wa"   // WhatsApp
)

var normalKeys = []keymap.Binding{
	// ---- moving
	{Seq: "j", Alt: []string{"Down"}, Act: "down", Desc: "Move to the next thread, chat or (picking) message (5j: five)", Count: true},
	{Seq: "k", Alt: []string{"Up"}, Act: "up", Desc: "Move to the previous thread, chat or (picking) message", Count: true},
	{Seq: "h", Act: "focus.list", Desc: "Move to the list"},
	{Seq: "l", Act: "focus.open", Desc: "Move to what's open"},
	{Seq: "g g", Alt: []string{"Home"}, Act: "top", Desc: "Jump to the first thread, chat or message (5gg: the fifth)", Count: true},
	{Seq: "G", Alt: []string{"End"}, Act: "bottom", Desc: "Jump to the last thread, chat or message (5G: the fifth)", Count: true},
	{Seq: "L", Act: "place.next", Desc: "Go to the next place (Mail, Chat, WhatsApp, the AI requests)", Count: true},
	{Seq: "H", Act: "place.prev", Desc: "Go to the previous place", Count: true},
	{Seq: "g t", Act: "place.goto", Desc: "Go to the next place (2gt: Chat; 1 Mail, 3 WhatsApp, 4 the AI requests)", Count: true},
	{Seq: "g T", Act: "place.prev", Desc: "Go to the previous place", Count: true},
	{Seq: "Alt+m", Act: "place.mail", Desc: "Go to Mail", Hide: true},
	{Seq: "Alt+c", Act: "place.chat", Desc: "Go to Google Chat", Hide: true},
	{Seq: "Alt+w", Act: "place.wa", Desc: "Go to WhatsApp", Hide: true},
	{Seq: "] ]", Alt: []string{"Tab"}, Act: "section.next", Desc: "Switch to the next section (Unread, Groups, Sent…)", Count: true},
	{Seq: "[ [", Alt: []string{"Shift+Tab"}, Act: "section.prev", Desc: "Switch to the previous section", Count: true},
	{Seq: "Ctrl+n", Act: "chat.next", Desc: "Switch to the next chat, staying in the conversation", Count: true},
	{Seq: "Ctrl+p", Act: "chat.prev", Desc: "Switch to the previous chat, staying in the conversation", Count: true},
	{Seq: "J", Act: "msg.next", Desc: "Move to the next message in what's open (mail: the next thread)", Count: true},
	{Seq: "K", Act: "msg.prev", Desc: "Move to the previous message in what's open (mail: the previous thread)", Count: true},
	{Seq: "n", Act: "unread.next", Desc: "Jump to the next unread thread or chat", Count: true},
	{Seq: "N", Act: "unread.prev", Desc: "Jump to the previous unread thread or chat", Count: true},
	{Seq: "Ctrl+d", Act: "scroll.half-down", Desc: "Scroll what's open half a page down", Count: true},
	{Seq: "Ctrl+u", Act: "scroll.half-up", Desc: "Scroll what's open half a page up", Count: true},
	{Seq: "Ctrl+f", Alt: []string{"PgDn"}, Act: "scroll.page-down", Desc: "Scroll what's open a page down", Count: true},
	{Seq: "Ctrl+b", Alt: []string{"PgUp"}, Act: "scroll.page-up", Desc: "Scroll what's open a page up", Count: true},
	{Seq: "Ctrl+j", Act: "scroll.down", Desc: "Scroll what's open 3 lines down (scroll_lines sets how many)", Count: true},
	{Seq: "Ctrl+k", Act: "scroll.up", Desc: "Scroll what's open 3 lines up", Count: true},
	{Seq: "Ctrl+e", Act: "scroll.line-down", Desc: "Scroll what's open a line down", Count: true},
	{Seq: "Ctrl+y", Act: "scroll.line-up", Desc: "Scroll what's open a line up", Count: true},
	{Seq: "z z", Act: "view.middle", Desc: "Scroll the picked message to the middle", Where: "chat wa"},
	{Seq: "z t", Act: "view.top", Desc: "Scroll the picked message to the top", Where: "chat wa"},
	{Seq: "z b", Act: "view.bottom", Desc: "Scroll the picked message to the bottom", Where: "chat wa"},
	{Seq: "g d", Alt: []string{"%"}, Act: "msg.quote", Desc: "Jump to the message the picked reply quotes", Where: "chat wa"},
	{Seq: "Backspace", Act: "msg.back", Desc: "Jump back to the reply you came from (after gd)", Where: "chat wa"},
	{Seq: "Enter", Act: "open", Desc: "Open the thread or chat (picking: a photo full size, a reply's original)"},
	{Seq: "q", Act: "back", Desc: "Go back a step (q at the top quits)"},
	{Seq: "Esc", Act: "back.esc", Desc: "Go back a step: close what's open, clear the picks, the filter, the search"},
	{Seq: "/", Alt: []string{"Space f f"}, Act: "find", Desc: "Search everything: actions, chats, people, messages and mail (Gmail's search words work)"},
	{Seq: "?", Alt: []string{"Space ?"}, Act: "help", Desc: "Show every key"},
	{Seq: ":", Act: "command", Desc: "Run a command: archive, trash, read, star, label, mute, account, sync, q (Tab completes)"},

	// ---- acting: Vim's verbs
	{Seq: "i", Act: "write", Desc: "Start writing: a message in a chat, a reply in mail"},
	{Seq: "a", Act: "write.all", Desc: "Start writing: a message in a chat, a reply to everyone in mail"},
	{Seq: "g i", Act: "write", Desc: "Go back into the message box, to what you were writing"},
	{Seq: "r", Act: "reply", Desc: "Send a reply (in chat: to the picked message, quoting it)"},
	{Seq: "R", Act: "reply.all", Desc: "Send a reply to everyone on the thread (WhatsApp group: privately; a failed send: again)"},
	{Seq: "o", Act: "new", Desc: "Create a new mail, or a message in this chat"},
	{Seq: "O", Act: "new.convo", Desc: "Start a conversation with someone"},
	{Seq: "d d", Alt: []string{"Space d d"}, Act: "delete", Desc: "Trash the thread, or delete the picked message (asks first)"},
	{Seq: "e", Alt: []string{"Space m a"}, Act: "archive", Desc: "Move the thread or WhatsApp chat out of the inbox (archive; u undoes it)"},
	{Seq: "y y", Alt: []string{"Space y y"}, Act: "copy", Desc: "Copy the picked message (else the link to what's open)"},
	{Seq: "p", Act: "paste", Desc: "Paste the clipboard into the message box (text, or a picture as a file)", Where: "chat wa"},
	{Seq: "~", Act: "read.toggle", Desc: "Toggle read / unread (or everything picked)"},
	{Seq: "u", Act: "undo", Desc: "Undo the last change, or cancel a send in its undo window"},
	{Seq: "Ctrl+r", Act: "redo", Desc: "Run what u undid again (redo; a send is never redone)"},
	{Seq: ".", Act: "repeat", Desc: "Run the last change again on the item you're on (repeat: j. does it to the next)"},
	{Seq: "x", Act: "delete", Desc: "Trash the thread, or delete the picked message (asks first, like dd)"},
	{Seq: "d j", Act: "delete.down", Desc: "Trash this thread and the next ones (3dj or d3j: three more; asks first)", Where: inMail, Count: true},
	{Seq: "d k", Act: "delete.up", Desc: "Trash this thread and the ones above (asks first)", Where: inMail, Count: true},
	{Seq: "d G", Act: "delete.bottom", Desc: "Trash every thread from here to the end of the list (asks first)", Where: inMail},
	{Seq: "d g g", Act: "delete.top", Desc: "Trash every thread from the top of the list to here (asks first)", Where: inMail},
	{Seq: "g v", Act: "visual.again", Desc: "Pick the last range again (VISUAL)"},
	{Seq: "Ctrl+o", Alt: []string{"[ j"}, Act: "jump.back", Desc: "Jump back to where you were before (the jump list: opens, gg, G, marks)", Count: true},
	{Seq: "] j", Act: "jump.forward", Desc: "Jump forward again in the jump list", Count: true},
	{Seq: "*", Act: "sender.next", Desc: "Jump to the next message from the picked message's sender (in a chat)"},
	{Seq: "#", Act: "sender.prev", Desc: "Jump to the previous message from the picked message's sender (in a chat)"},
	{Seq: "v", Act: "visual", Desc: "Pick several (VISUAL): j/k, gg, G grow it, then d, e, ~ or y act on all"},
	{Seq: "V", Act: "visual.all", Desc: "Pick the whole list (again clears)"},
	{Seq: "z a", Alt: []string{"Space u z"}, Act: "fold", Desc: "Toggle every message in the thread open / folded", Where: inMail},
	{Seq: "z M", Act: "fold.close", Desc: "Toggle every message in the thread folded", Where: inMail},
	{Seq: "z R", Act: "fold.open", Desc: "Toggle every message in the thread open", Where: inMail},
	{Seq: "z o", Act: "fold.open", Desc: "Open the folded messages of the thread (mail has no picked message: all of them)", Where: inMail},
	{Seq: "z c", Act: "fold.close", Desc: "Close the messages of the thread to one line each (all of them)", Where: inMail},
	{Seq: "z q", Alt: []string{"Space u q"}, Act: "quoted", Desc: "Toggle the quoted text in the thread", Where: inMail},
	{Seq: "z a", Alt: []string{"Space u z"}, Act: "msg.fold", Desc: "Toggle the picked long message open / folded (none picked: every one)", Where: "chat wa"},
	{Seq: "z o", Act: "msg.open", Desc: "Open the picked long message in full", Where: "chat wa"},
	{Seq: "z c", Act: "msg.close", Desc: "Close the picked long message to its first lines", Where: "chat wa"},
	{Seq: "z R", Act: "msg.open.all", Desc: "Open every long message in the chat", Where: "chat wa"},
	{Seq: "z M", Act: "msg.close.all", Desc: "Close every long message in the chat to its first lines", Where: "chat wa"},
	{Seq: "g f", Alt: []string{"Space o f"}, Act: "files.open", Desc: "Open the attachments (kept in ~/Downloads/narada)"},
	{Seq: "g F", Alt: []string{"Space y f"}, Act: "files.save", Desc: "Save the attachments (in ~/Downloads/narada)"},
	{Seq: "g x", Alt: []string{"Space o l"}, Act: "link", Desc: "Open a link from what's open, by its number"},
	{Seq: "A", Alt: []string{"Space k d"}, Act: "ai.write", Desc: "Send this chat or mail to Claude to write your reply (you use, edit, change or drop its draft)"},

	// marks: m{a-z} sets one, '{a-z} jumps to it (kept across runs)
	{Seq: "m a", Act: "mark.set.a", Desc: "Set mark a on this thread or chat", Hide: true},
	{Seq: "' a", Act: "mark.go.a", Desc: "Jump to mark a", Hide: true},
	{Seq: "m b", Act: "mark.set.b", Desc: "Set mark b on this thread or chat", Hide: true},
	{Seq: "' b", Act: "mark.go.b", Desc: "Jump to mark b", Hide: true},
	{Seq: "m c", Act: "mark.set.c", Desc: "Set mark c on this thread or chat", Hide: true},
	{Seq: "' c", Act: "mark.go.c", Desc: "Jump to mark c", Hide: true},
	{Seq: "m d", Act: "mark.set.d", Desc: "Set mark d on this thread or chat", Hide: true},
	{Seq: "' d", Act: "mark.go.d", Desc: "Jump to mark d", Hide: true},
	{Seq: "m e", Act: "mark.set.e", Desc: "Set mark e on this thread or chat", Hide: true},
	{Seq: "' e", Act: "mark.go.e", Desc: "Jump to mark e", Hide: true},
	{Seq: "m f", Act: "mark.set.f", Desc: "Set mark f on this thread or chat", Hide: true},
	{Seq: "' f", Act: "mark.go.f", Desc: "Jump to mark f", Hide: true},
	{Seq: "m g", Act: "mark.set.g", Desc: "Set mark g on this thread or chat", Hide: true},
	{Seq: "' g", Act: "mark.go.g", Desc: "Jump to mark g", Hide: true},
	{Seq: "m h", Act: "mark.set.h", Desc: "Set mark h on this thread or chat", Hide: true},
	{Seq: "' h", Act: "mark.go.h", Desc: "Jump to mark h", Hide: true},
	{Seq: "m i", Act: "mark.set.i", Desc: "Set mark i on this thread or chat", Hide: true},
	{Seq: "' i", Act: "mark.go.i", Desc: "Jump to mark i", Hide: true},
	{Seq: "m j", Act: "mark.set.j", Desc: "Set mark j on this thread or chat", Hide: true},
	{Seq: "' j", Act: "mark.go.j", Desc: "Jump to mark j", Hide: true},
	{Seq: "m k", Act: "mark.set.k", Desc: "Set mark k on this thread or chat", Hide: true},
	{Seq: "' k", Act: "mark.go.k", Desc: "Jump to mark k", Hide: true},
	{Seq: "m l", Act: "mark.set.l", Desc: "Set mark l on this thread or chat", Hide: true},
	{Seq: "' l", Act: "mark.go.l", Desc: "Jump to mark l", Hide: true},
	{Seq: "m m", Act: "mark.set.m", Desc: "Set mark m on this thread or chat", Hide: true},
	{Seq: "' m", Act: "mark.go.m", Desc: "Jump to mark m", Hide: true},
	{Seq: "m n", Act: "mark.set.n", Desc: "Set mark n on this thread or chat", Hide: true},
	{Seq: "' n", Act: "mark.go.n", Desc: "Jump to mark n", Hide: true},
	{Seq: "m o", Act: "mark.set.o", Desc: "Set mark o on this thread or chat", Hide: true},
	{Seq: "' o", Act: "mark.go.o", Desc: "Jump to mark o", Hide: true},
	{Seq: "m p", Act: "mark.set.p", Desc: "Set mark p on this thread or chat", Hide: true},
	{Seq: "' p", Act: "mark.go.p", Desc: "Jump to mark p", Hide: true},
	{Seq: "m q", Act: "mark.set.q", Desc: "Set mark q on this thread or chat", Hide: true},
	{Seq: "' q", Act: "mark.go.q", Desc: "Jump to mark q", Hide: true},
	{Seq: "m r", Act: "mark.set.r", Desc: "Set mark r on this thread or chat", Hide: true},
	{Seq: "' r", Act: "mark.go.r", Desc: "Jump to mark r", Hide: true},
	{Seq: "m s", Act: "mark.set.s", Desc: "Set mark s on this thread or chat", Hide: true},
	{Seq: "' s", Act: "mark.go.s", Desc: "Jump to mark s", Hide: true},
	{Seq: "m t", Act: "mark.set.t", Desc: "Set mark t on this thread or chat", Hide: true},
	{Seq: "' t", Act: "mark.go.t", Desc: "Jump to mark t", Hide: true},
	{Seq: "m u", Act: "mark.set.u", Desc: "Set mark u on this thread or chat", Hide: true},
	{Seq: "' u", Act: "mark.go.u", Desc: "Jump to mark u", Hide: true},
	{Seq: "m v", Act: "mark.set.v", Desc: "Set mark v on this thread or chat", Hide: true},
	{Seq: "' v", Act: "mark.go.v", Desc: "Jump to mark v", Hide: true},
	{Seq: "m w", Act: "mark.set.w", Desc: "Set mark w on this thread or chat", Hide: true},
	{Seq: "' w", Act: "mark.go.w", Desc: "Jump to mark w", Hide: true},
	{Seq: "m x", Act: "mark.set.x", Desc: "Set mark x on this thread or chat", Hide: true},
	{Seq: "' x", Act: "mark.go.x", Desc: "Jump to mark x", Hide: true},
	{Seq: "m y", Act: "mark.set.y", Desc: "Set mark y on this thread or chat", Hide: true},
	{Seq: "' y", Act: "mark.go.y", Desc: "Jump to mark y", Hide: true},
	{Seq: "m z", Act: "mark.set.z", Desc: "Set mark z on this thread or chat", Hide: true},
	{Seq: "' z", Act: "mark.go.z", Desc: "Jump to mark z", Hide: true},

	// ---- Space: the reserved groups
	{Seq: "Space Space", Act: "palette", Desc: "Search every action and run one (the palette)"},
	{Seq: "Space q", Act: "quit", Desc: "Quit narada"},
	{Seq: "Space t m", Act: "place.mail", Desc: "Go to Mail"},
	{Seq: "Space t c", Act: "place.chat", Desc: "Go to Google Chat"},
	{Seq: "Space t w", Act: "place.wa", Desc: "Go to WhatsApp"},
	{Seq: "Space t a", Act: "place.ai", Desc: "Go to the AI requests"},
	{Seq: "Space y l", Act: "copy.link", Desc: "Copy the link to the thread or chat"},
	{Seq: "Space f h", Act: "find.here", Desc: "Search inside what's open (then n / N step through the matches); in the list: only this section"},
	{Seq: "Space f u", Alt: []string{"Space g u"}, Act: "go.unread", Desc: "Show only the unread (the Unread section)"},
	{Seq: "Space r r", Act: "sync", Desc: "Reload mail and chat from Google now (sync)"},
	{Seq: "Space r o", Act: "older", Desc: "Update the list with older mail", Where: inMail},
	{Seq: "Space r +", Act: "older.more", Desc: "Update how many older mails load at a time: more (10, 25, 50, 100)", Where: inMail},
	{Seq: "Space r -", Act: "older.fewer", Desc: "Update how many older mails load at a time: fewer", Where: inMail},
	{Seq: "Space , l", Act: "wa.link", Desc: "Connect this machine to WhatsApp with a QR code (link)", Where: inWA},
	{Seq: "Space , L", Act: "wa.link-code", Desc: "Connect this machine to WhatsApp with your phone number and a code (link)", Where: inWA},
	{Seq: "Space a n", Act: "new", Desc: "Create a new mail, or a message in this chat"},
	{Seq: "Space a c", Act: "new.convo", Desc: "Start a conversation with someone"},
	{Seq: "Space a f", Act: "file.send", Desc: "Send a file into this chat", Where: "chat wa"},
	{Seq: "Space a g", Act: "section.save", Desc: "Save the current search as a section", Where: inMail},
	{Seq: "Space d s", Act: "section.remove", Desc: "Remove the current saved-search section (asks first)", Where: inMail},
	{Seq: "Space e e", Act: "msg.edit", Desc: "Edit your picked message (WhatsApp: within 15 minutes)", Where: "chat wa"},
	{Seq: "Space e r", Act: "chat.rename", Desc: "Rename the chat (spaces and groups for everyone)", Where: "chat wa"},
	{Seq: "Space e t", Act: "send.later", Desc: "Set another time for a scheduled send (in Scheduled)"},
	{Seq: "Space e d", Act: "wa.disappear", Desc: "Set the chat's messages to disappear after 24 hours (again: 7 days, 90 days, off)", Where: inWA},
	{Seq: "Space o b", Act: "browser", Desc: "Open the thread or chat in the browser"},
	{Seq: "Space g i", Act: "go.inbox", Desc: "Go to the Inbox section"},
	{Seq: "Space g s", Act: "go.starred", Desc: "Go to the Starred section", Where: inMail},
	{Seq: "Space g S", Act: "go.sent", Desc: "Go to the Sent section"},
	{Seq: "Space g l", Act: "go.scheduled", Desc: "Go to the Scheduled section"},
	{Seq: "Space g t", Act: "go.status", Desc: "Go to the WhatsApp statuses", Where: inWA},
	{Seq: "Space g r", Act: "go.archived", Desc: "Go to the archived WhatsApp chats", Where: inWA},
	{Seq: "Space g a", Act: "account", Desc: "Switch account (all, then each)"},
	{Seq: "Space m s", Act: "star", Desc: "Toggle the star on the thread or picked message"},
	{Seq: "Space m l", Act: "label", Desc: "Toggle a label on the thread (add or remove one)", Where: inMail},
	{Seq: "Space m p", Act: "pin", Desc: "Toggle the chat pinned on top (WhatsApp: on your phone too; picking: the message)", Where: "chat wa"},
	{Seq: "Space m r", Act: "read", Desc: "Set it as read (or everything picked)"},
	{Seq: "Space m u", Act: "unread", Desc: "Set it as unread (or everything picked)"},
	{Seq: "Space m R", Act: "read.all", Desc: "Set everything in this list as read"},
	{Seq: "Space m z", Act: "wa.mute", Desc: "Set the chat muted for 8 hours (again: a week, then always, then unmuted)", Where: inWA},
	{Seq: "Space m b", Act: "wa.block", Desc: "Set the person in this chat as blocked (asks first; again unblocks)", Where: inWA},
	{Seq: "Space u f", Act: "ui.zoom", Desc: "Toggle what's open across the whole width (zoom)"},
	{Seq: "Space u p", Act: "ui.preview", Desc: "Toggle the preview pane beside the list"},
	{Seq: "Space u c", Act: "ui.compact", Desc: "Toggle a compact list (no blank rows around each row)"},
	{Seq: "Space u i", Act: "ui.images", Desc: "Toggle pictures inline in what's open"},
	{Seq: "Space k k", Act: "ai.ask", Desc: "Open a question to Claude, with this thread or chat as context"},
	{Seq: "Space k s", Act: "ai.summary", Desc: "Show Claude's summary of the thread or the chat's recent messages"},
	{Seq: "Space k t", Act: "ai.digest", Desc: "Show today's digest: what came in and what needs you"},

	// ---- Space: narada's own groups (free letters)
	{Seq: "Space c i", Act: "wa.info", Desc: "Show the group's people, admins and description", Where: inWA},
	{Seq: "Space c v", Act: "wa.vote", Desc: "Set your vote in the picked WhatsApp poll", Where: inWA},
	{Seq: "Space i f", Act: "msg.forward", Desc: "Send the thread or the picked WhatsApp message on to someone (forward)"},
	{Seq: "Space i e", Act: "msg.react", Desc: "Toggle a reaction on the picked message, or send one to a status", Where: "chat wa"},
	{Seq: "Space i p", Act: "msg.private", Desc: "Send a private reply to the picked WhatsApp group message", Where: inWA},
	{Seq: "Space i R", Act: "send.again", Desc: "Send a failed mail or message again"},
	{Seq: "Space w h", Act: "focus.list", Desc: "Move to the list"},
	{Seq: "Space w l", Act: "focus.open", Desc: "Move to what's open"},
	{Seq: "Space w w", Act: "focus.other", Desc: "Move to the other pane"},
	{Seq: "Space x x", Act: "place.ai", Desc: "Go to the AI requests (y sends, e edits, n n declines)"},

	// ---- moved, for one release: they say where they went
	{Seq: "s", Moved: "/"},
	{Seq: "c", Moved: "o"},
	{Seq: "f", Moved: "Space i f"},
	{Seq: "M", Moved: "Space m u (or ~)"},
	{Seq: "t", Moved: "Space e t"},
	{Seq: "U", Moved: "Space a f"},
	{Seq: "b", Moved: "Space o b"},
	{Seq: "W", Moved: "Space a g"},
	{Seq: "X", Moved: "Space d s (WhatsApp: Space e d)"},
	{Seq: "Z", Moved: "Space m z"},
	{Seq: "I", Moved: "Space c i"},
	{Seq: "B", Moved: "Space m b"},
	{Seq: "P", Moved: "Space c v"},
	{Seq: "E", Moved: "Space e e (mail: z a)"},
	{Seq: "D", Moved: "Space k d"},
	{Seq: "S", Moved: "Space k s"},
	{Seq: "T", Moved: "Space k t"},
	{Seq: "@", Moved: "Space k k"},
	{Seq: "Y", Moved: "Space y l"},
	{Seq: "Q", Moved: "z q"},
	{Seq: "+", Moved: "Space r +"},
	{Seq: "-", Moved: "Space r -"},
	{Seq: "Alt+r", Moved: "Space m R"},
	{Seq: "Ctrl+x", Moved: "V"},
}

// The keys of the other screens. The screens read them themselves; here
// they are only for the key sheet, the README and the keybinding search
// (no Act: the engine never fires them).
var otherKeys = []keymap.Binding{
	// NORMAL keys written as a range (each letter is its own binding above)
	{Seq: "m a-z", Desc: "Set a mark (a to z) on this thread or chat; it lasts across runs"},
	{Seq: "' a-z", Desc: "Jump to the mark (a to z), wherever it is"},
	// VISUAL
	{Seq: "j / k / gg / G", Desc: "Grow the range down / up / to the top / to the end", Where: "visual", Modes: []vmode.Mode{vmode.Visual}},
	{Seq: "d", Desc: "Move everything picked to the trash (asks first)", Where: "visual", Modes: []vmode.Mode{vmode.Visual}},
	{Seq: "e", Desc: "Move everything picked out of the inbox (archive; u undoes it)", Where: "visual", Modes: []vmode.Mode{vmode.Visual}},
	{Seq: "~", Desc: "Toggle everything picked read / unread", Where: "visual", Modes: []vmode.Mode{vmode.Visual}},
	{Seq: "y", Desc: "Copy the subjects or chat names picked", Where: "visual", Modes: []vmode.Mode{vmode.Visual}},
	{Seq: "Esc / v", Desc: "Leave VISUAL (nothing stays picked)", Where: "visual", Modes: []vmode.Mode{vmode.Visual}},
	// INSERT: the message box
	{Seq: "Esc", Desc: "Switch to the box's own NORMAL (the text and the cursor stay; Esc again leaves the box)", Where: "writing", Modes: []vmode.Mode{vmode.Insert}},
	{Seq: "Ctrl+x Ctrl+e", Desc: "Edit the message in your editor ($EDITOR), then come back", Where: "writing", Modes: []vmode.Mode{vmode.Insert}},
	{Seq: "Enter", Desc: "Send the message and keep writing the next one", Where: "writing", Modes: []vmode.Mode{vmode.Insert}},
	{Seq: "Alt+Enter / Ctrl+j", Desc: "Start a new line in the message", Where: "writing", Modes: []vmode.Mode{vmode.Insert}},
	{Seq: "Ctrl+o", Desc: "Add a file to the message", Where: "writing", Modes: []vmode.Mode{vmode.Insert}},
	{Seq: "Ctrl+v", Desc: "Paste a copied image (a screenshot) into the message", Where: "writing", Modes: []vmode.Mode{vmode.Insert}},
	{Seq: "Ctrl+t", Desc: "Send the message later (tomorrow 9am, fri 6pm, in 2h)", Where: "writing", Modes: []vmode.Mode{vmode.Insert}},
	{Seq: "Ctrl+g", Desc: "Edit what you typed with Claude: polish it (your words, fixed up)", Where: "writing", Modes: []vmode.Mode{vmode.Insert}},
	{Seq: "Ctrl+r", Desc: "Record a voice note on WhatsApp (again stops and adds it; Enter sends it, Esc drops it)", Where: "writing", Modes: []vmode.Mode{vmode.Insert}},
	{Seq: "Ctrl+p", Desc: "Send what you wrote as a WhatsApp poll: the question, then one option per line", Where: "writing", Modes: []vmode.Mode{vmode.Insert}},
	{Seq: "@", Desc: "Pick someone to tag on WhatsApp from the list (Up/Down, Enter or Tab; Esc for none)", Where: "writing", Modes: []vmode.Mode{vmode.Insert}},
	{Seq: "Left / Right / Alt+b / Alt+f / Ctrl+a / Ctrl+e", Desc: "Move the cursor by letter, word, line start / end", Where: "writing", Modes: []vmode.Mode{vmode.Insert}},
	{Seq: "Ctrl+w / Ctrl+k / Ctrl+u", Desc: "Delete the word before the cursor / to the end / the whole message", Where: "writing", Modes: []vmode.Mode{vmode.Insert}},
	// the message box's NORMAL (Esc while writing; the compose body too)
	{Seq: "h / l / w / b / e / 0 / ^ / $", Desc: "Move the cursor in the message by a letter, a word, to the line's start / end", Where: "boxnormal"},
	{Seq: "j / k / gg / G", Desc: "Move the cursor a line down / up, to the first / last line", Where: "boxnormal"},
	{Seq: "x / dw / dd / D", Desc: "Delete a letter / a word / the line / to the end of the line", Where: "boxnormal"},
	{Seq: "cw / ciw / C", Desc: "Delete a word / the word under the cursor / the rest of the line and type in its place (cw, ciw, C)", Where: "boxnormal"},
	{Seq: "u / Ctrl+r", Desc: "Undo / redo a change to the message", Where: "boxnormal"},
	{Seq: "i / a / I / A / o", Desc: "Start typing again: here, after, at the line's start / end, on a new line", Where: "boxnormal"},
	{Seq: "p", Desc: "Paste the copied text after the cursor", Where: "boxnormal"},
	{Seq: "Enter", Desc: "Send the message from the box's NORMAL (the compose screen: Ctrl+s)", Where: "boxnormal"},
	{Seq: "Space e e", Desc: "Edit the message in your editor ($EDITOR) from the box's NORMAL", Where: "boxnormal"},
	{Seq: "Esc / q", Desc: "Leave the box (what you wrote stays for this chat)", Where: "boxnormal"},
	// Claude writing a reply
	{Seq: "1 - 4", Desc: "Pick one of the four suggested asks for Claude (reply, say yes, say no, …)", Where: "aidraft"},
	{Seq: "Tab", Desc: "Toggle more context for Claude (older messages, or your sent mail for tone)", Where: "aidraft"},
	{Seq: "y", Desc: "Send Claude's draft as yours (through the undo window)", Where: "aidraft"},
	{Seq: "e", Desc: "Edit Claude's draft yourself before sending", Where: "aidraft"},
	{Seq: "c", Desc: "Request changes to Claude's draft (shorter, warmer, in Hindi, or your own words)", Where: "aidraft"},
	{Seq: "Left / Right", Desc: "Show the previous / next version of the draft", Where: "aidraft"},
	{Seq: "n / Esc", Desc: "Cancel Claude's draft (nothing is sent)", Where: "aidraft"},
	// the compose screen
	{Seq: "Tab / Shift+Tab", Desc: "Move to the next / previous field (Tab adds what you typed first)", Where: "compose", Modes: []vmode.Mode{vmode.Insert}},
	{Seq: "Up / Down", Desc: "Pick a contact from the suggestions", Where: "compose", Modes: []vmode.Mode{vmode.Insert}},
	{Seq: "Enter / ,", Desc: "Add the picked contact or typed address, and stay for more", Where: "compose", Modes: []vmode.Mode{vmode.Insert}},
	{Seq: "Backspace", Desc: "Take the last recipient off (in an empty To, Cc or Bcc)", Where: "compose", Modes: []vmode.Mode{vmode.Insert}},
	{Seq: "Ctrl+s", Desc: "Send the mail (after the undo window)", Where: "compose", Modes: []vmode.Mode{vmode.Insert}},
	{Seq: "Ctrl+t", Desc: "Send the mail later (pick or type when: tomorrow 9am, fri 6pm, in 2h)", Where: "compose", Modes: []vmode.Mode{vmode.Insert}},
	{Seq: "Ctrl+d", Desc: "Save the mail as a Gmail draft and close it", Where: "compose", Modes: []vmode.Mode{vmode.Insert}},
	{Seq: "Ctrl+o", Desc: "Add files to the mail (x takes one off in Files)", Where: "compose", Modes: []vmode.Mode{vmode.Insert}},
	{Seq: "Ctrl+e", Desc: "Edit the whole mail in your editor, then come back", Where: "compose", Modes: []vmode.Mode{vmode.Insert}},
	{Seq: "Ctrl+q", Desc: "Toggle the quoted text under a reply or forward", Where: "compose", Modes: []vmode.Mode{vmode.Insert}},
	{Seq: "Left / Right", Desc: "Select a recipient pill (in an empty To, Cc or Bcc; Backspace takes it off)", Where: "compose", Modes: []vmode.Mode{vmode.Insert}},
	{Seq: "Ctrl+b", Desc: "Toggle the Cc and Bcc fields", Where: "compose", Modes: []vmode.Mode{vmode.Insert}},
	{Seq: "Ctrl+g", Desc: "Toggle your Gmail signature under the mail", Where: "compose", Modes: []vmode.Mode{vmode.Insert}},
	{Seq: "Ctrl+f", Desc: "Switch the account the mail is sent from", Where: "compose", Modes: []vmode.Mode{vmode.Insert}},
	{Seq: "PgUp / PgDn", Desc: "Scroll the message you're replying to", Where: "compose", Modes: []vmode.Mode{vmode.Insert}},
	{Seq: "Esc", Desc: "Cancel the mail (asks first once something is written)", Where: "compose", Modes: []vmode.Mode{vmode.Insert}},
	// the finder (/)
	{Seq: "Up / Down", Desc: "Move between the results (Ctrl+p / Ctrl+n too)", Where: "search", Modes: []vmode.Mode{vmode.Insert}},
	{Seq: "Enter", Desc: "Open the result, or do the action", Where: "search", Modes: []vmode.Mode{vmode.Insert}},
	{Seq: "Tab", Desc: "Switch where it looks: here, or everywhere", Where: "search", Modes: []vmode.Mode{vmode.Insert}},
	{Seq: "Ctrl+u", Desc: "Clear the search", Where: "search", Modes: []vmode.Mode{vmode.Insert}},
	{Seq: "Esc", Desc: "Close the search", Where: "search", Modes: []vmode.Mode{vmode.Insert}},
	// reviewing what you wrote in the editor
	{Seq: "y", Desc: "Send the message you wrote (after the undo window)", Where: "review"},
	{Seq: "t", Desc: "Send the message later (pick or type when)", Where: "review"},
	{Seq: "e", Desc: "Edit the message again in your editor", Where: "review"},
	{Seq: "+", Desc: "Add a file to the message", Where: "review"},
	{Seq: "-", Desc: "Take the last file off the message (+ adds it back)", Where: "review"},
	{Seq: "d", Desc: "Save the message as a Gmail draft instead of sending", Where: "review"},
	{Seq: "n", Desc: "Delete the message you wrote (discard; n twice, so a stray n keeps it)", Where: "review"},
	// an agent's message waiting for you
	{Seq: "y", Desc: "Send the message an agent wrote (after the undo window)", Where: "approve"},
	{Seq: "e", Desc: "Edit the agent's message in your editor first", Where: "approve"},
	{Seq: "u", Desc: "Undo your edit of the agent's message", Where: "approve"},
	{Seq: "d", Desc: "Toggle the changes marked in your edit of the agent's message", Where: "approve"},
	{Seq: "s", Desc: "Move to the next message waiting (leaves this one waiting)", Where: "approve"},
	{Seq: "J / K", Desc: "Scroll the details of the agent's message", Where: "approve"},
	{Seq: "n", Desc: "Cancel the agent's message (decline; nothing is sent)", Where: "approve"},
	{Seq: "q", Desc: "Leave the agent's message waiting and go back", Where: "approve"},
	// first start
	{Seq: "Tab", Desc: "Switch to the next path completion (Enter on a folder opens it)", Where: "setup", Modes: []vmode.Mode{vmode.Insert}},
	{Seq: "Enter", Desc: "Save the OAuth client file at this path, or start the Google sign-in", Where: "setup", Modes: []vmode.Mode{vmode.Insert}},
	{Seq: "Ctrl+t", Desc: "Enter the OAuth client ID and secret by hand instead", Where: "setup", Modes: []vmode.Mode{vmode.Insert}},
	{Seq: "Ctrl+o", Desc: "Open the Google Cloud console credentials page", Where: "setup", Modes: []vmode.Mode{vmode.Insert}},
	{Seq: "Ctrl+g", Desc: "Show the steps for creating the OAuth client", Where: "setup", Modes: []vmode.Mode{vmode.Insert}},
	{Seq: "Ctrl+d", Desc: "Start the demo on example mail instead", Where: "setup", Modes: []vmode.Mode{vmode.Insert}},
	{Seq: "n", Desc: "Rename the account before signing in", Where: "setup"},
	{Seq: "c", Desc: "Pick a different OAuth client file", Where: "setup"},
	{Seq: "y", Desc: "Copy the sign-in link", Where: "setup"},
}

// narKeys is every key of narada.
var narKeys = keymap.New("narada", normalKeys, otherKeys).
	Group("Space c", "This chat").
	Group("Space i", "This message").
	Group("Space w", "Window").
	Group("Space x", "AI requests").
	Where(inMail, "In Mail").
	Where(inChat, "In Google Chat").
	Where(inWA, "On WhatsApp").
	Where("visual", "Picking several (VISUAL)").
	Where("writing", "Writing a message (INSERT)").
	Where("boxnormal", "The message box's NORMAL (Esc while writing)").
	Where("aidraft", "Claude writing your reply").
	Where("compose", "Writing a mail").
	Where("search", "Searching (/)").
	Where("review", "Reviewing what you wrote").
	Where("approve", "An agent's message waiting").
	Where("setup", "First start")

// where is the view the engine is told: the place you're in.
func (m Model) where() string {
	switch {
	case m.tab == tabMail:
		return inMail
	case m.wa:
		return inWA
	}
	return inChat
}
