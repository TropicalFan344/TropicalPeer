package protocol

import (
	"errors"
	"strings"
	"testing"
)

func TestCleanText(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		err      error
	}{
		{in: "https://example.com", want: "https://example.com"},
		{in: "line one\r\nline two\rthree", want: "line one\nline two\nthree"},
		{in: "\n\nkept\tindent\n\n", want: "kept\tindent"},
		{in: "a\x00b\x1bc\x7fd", want: "abcd"},
		{in: "https://evil.example/\u202Emoc.knab", want: "https://evil.example/moc.knab"},
		{in: "x\u2066y\u2069z", want: "xyz"},
		{in: "\uFEFFfrom a file", want: "from a file"},
		{in: "עברית\u200F", want: "עברית\u200F"},
		{in: "  \n\t ", err: ErrTextEmpty},
		{in: "\u202E", err: ErrTextEmpty},
		{in: "bad \xff utf-8", err: ErrTextInvalid},
		{in: strings.Repeat("x", MaxTextBytes+1), err: ErrTextTooLong},
	} {
		got, err := CleanText(tc.in)
		if !errors.Is(err, tc.err) || got != tc.want {
			t.Errorf("CleanText(%q) = %q, %v; want %q, %v", tc.in, got, err, tc.want, tc.err)
		}
	}
	if _, err := CleanText(strings.Repeat("x", MaxTextBytes)); err != nil {
		t.Errorf("a text of exactly MaxTextBytes: %v", err)
	}
}
