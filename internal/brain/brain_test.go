package brain

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateContent(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want error
	}{
		{"empty", "", ErrEmptyContent},
		{"spaces only", "   ", ErrBlankContent},
		{"mixed whitespace only", "\n\t ", ErrBlankContent},
		{"NUL byte", "a\x00b", ErrInvalidContent},
		{"invalid UTF-8", string([]byte{0xff, 0xfe}), ErrInvalidContent},
		{"too long", strings.Repeat("a", maxContentLen+1), ErrContentTooLong},
		{"plain", "hello", nil},
		{"padded", "  hello  ", nil},
		{"multibyte UTF-8", "héllo", nil},
		{"exactly max length", strings.Repeat("a", maxContentLen), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateContent(tt.in)
			if tt.want == nil {
				if err != nil {
					t.Fatalf("validateContent(%q) = %v, want nil", tt.in, err)
				}
				return
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("validateContent(%q) = %v, want %v", tt.in, err, tt.want)
			}
		})
	}
}

func TestNormalizeContent(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr error
	}{
		{"trims surrounding whitespace", "  hello\n", "hello", nil},
		{"rejects blank", "   ", "", ErrBlankContent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeContent(tt.in)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("normalizeContent(%q) error = %v, want nil", tt.in, err)
				}
			} else if !errors.Is(err, tt.wantErr) {
				t.Fatalf("normalizeContent(%q) error = %v, want %v", tt.in, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("normalizeContent(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestEscapeLikePattern(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"/foo_bar", `/foo\_bar`},
		{"/foo/bar", `/foo/bar`},
		{"/100%", `/100\%`},
		{`/a\b`, `/a\\b`},
		{"/foo_bar/baz_qux", `/foo\_bar/baz\_qux`},
	}
	for _, tt := range tests {
		if got := escapeLikePattern(tt.in); got != tt.want {
			t.Errorf("escapeLikePattern(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestLikePatternForDescendants(t *testing.T) {
	got := likePatternForDescendants("/foo_bar")
	want := `/foo\_bar/%`
	if got != want {
		t.Errorf("likePatternForDescendants(/foo_bar) = %q, want %q", got, want)
	}
}
