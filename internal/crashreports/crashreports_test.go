package crashreports

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSetting(t *testing.T) {
	dir := t.TempDir()
	s := Open(dir, func(string) string { return "" })
	if s.On() {
		t.Error("crash reports are off until the user turns them on")
	}
	if out, err := s.Command([]string{"on"}); err != nil || !s.On() || !strings.HasPrefix(out, "crash reports: on") {
		t.Errorf("on: %q %v", out, err)
	}
	if out, err := s.Command([]string{"off"}); err != nil || s.On() || !strings.HasPrefix(out, "crash reports: off") {
		t.Errorf("off: %q %v", out, err)
	}
	if _, err := s.Command([]string{"maybe"}); err == nil {
		t.Error("an unknown argument must fail")
	}

	dnt := Open(dir, func(k string) string { return map[string]string{"DO_NOT_TRACK": "1"}[k] })
	if _, err := dnt.Command([]string{"on"}); !errors.Is(err, ErrDisabled) || dnt.On() {
		t.Errorf("DO_NOT_TRACK: %v on=%v", err, dnt.On())
	}
	if !Open(dir, func(string) string { return "" }).On() {
		t.Error("the choice made under DO_NOT_TRACK is still kept for when it is unset")
	}
}

// A report is cut to what the service takes, without splitting a character.
func TestFit(t *testing.T) {
	r := Report{Message: strings.Repeat("é", MaxMessage), Stack: strings.Repeat("x", MaxStack+10), Operation: "op"}.Fit()
	if len(r.Message) > MaxMessage || !utf8.ValidString(r.Message) || !strings.HasSuffix(r.Message, "…") {
		t.Errorf("message: %d bytes, valid=%v", len(r.Message), utf8.ValidString(r.Message))
	}
	if len(r.Stack) > MaxStack || r.Operation != "op" {
		t.Errorf("stack %d bytes, operation %q", len(r.Stack), r.Operation)
	}
}
