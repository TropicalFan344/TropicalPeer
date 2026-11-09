package protocol

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Text is a short piece of text for the person on the other device: a
// link, an address, a code. It travels in a Hello and is answered with a
// JoinAck, like a resume request: OK means it was shown.
type Text struct {
	// ID is random (transfer.NewTextID). A sender that tries again after losing
	// the answer reuses it, so the receiver shows the text once.
	ID   string `json:"id"`
	Body string `json:"body"`
}

// MaxTextBytes bounds a Text's body. Anything longer goes as a file.
const MaxTextBytes = 32 << 10

// MaxNoteBytes bounds an Offer's note: it is shown with the offer, so it
// stays short. A longer text goes on its own.
const MaxNoteBytes = 4 << 10

var (
	ErrTextEmpty   = errors.New("the text is empty")
	ErrTextTooLong = errors.New("the text is too long")
	ErrTextInvalid = errors.New("the text isn't valid UTF-8")
)

// CleanText checks a text body and returns it ready to show. The sender
// calls it before sending and the receiver again on arrival, since it
// can't trust the sender: control characters are removed (newlines and
// tabs stay), and so are the bidirectional overrides and isolates, which
// could make one address look like another; line ends become "\n".
func CleanText(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", ErrTextInvalid
	}
	if len(s) > MaxTextBytes {
		return "", ErrTextTooLong
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r == '\r':
			return '\n'
		case isBidiControl(r), unicode.IsControl(r), r == '\uFEFF':
			return -1
		}
		return r
	}, s)
	if strings.TrimSpace(s) == "" {
		return "", ErrTextEmpty
	}
	return strings.Trim(s, "\n"), nil
}

// CleanNote is CleanText for an offer's note, which may be empty and is
// at most MaxNoteBytes.
func CleanNote(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	if len(s) > MaxNoteBytes {
		return "", ErrTextTooLong
	}
	s, err := CleanText(s)
	if errors.Is(err, ErrTextEmpty) {
		return "", nil
	}
	return s, err
}

// isBidiControl: the embeddings, overrides and isolates (U+202A–U+202E,
// U+2066–U+2069). The plain marks (U+200E, U+200F, U+061C) can't reorder
// text on their own, so they stay for languages that need them.
func isBidiControl(r rune) bool {
	return (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069)
}
