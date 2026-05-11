package util_test

import (
	"testing"
	"time"

	"github.com/lboyeon1223/gym-check-in/backend/internal/util"
)

func TestMaskPhone(t *testing.T) {
	got, err := util.MaskPhone("01012345678")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "010-****-5678" {
		t.Fatalf("unexpected mask: %q", got)
	}
}

func TestMaskPhone_RejectsBadInput(t *testing.T) {
	cases := []string{"", "0101234567", "010123456789", "010-1234-5678", "abcdefghijk"}
	for _, c := range cases {
		if _, err := util.MaskPhone(c); err == nil {
			t.Errorf("expected error for input %q", c)
		}
	}
}

func TestMaskBirthMD(t *testing.T) {
	d := time.Date(1990, time.April, 15, 0, 0, 0, 0, time.UTC)
	if got := util.MaskBirthMD(d); got != "**-04-15" {
		t.Fatalf("unexpected: %q", got)
	}
	// One-digit month/day stay zero-padded.
	d2 := time.Date(2001, time.January, 3, 12, 0, 0, 0, time.UTC)
	if got := util.MaskBirthMD(d2); got != "**-01-03" {
		t.Fatalf("zero pad failed: %q", got)
	}
}

func TestMemberIDDisplay(t *testing.T) {
	if got := util.MemberIDDisplay(1234); got != "#1234" {
		t.Fatalf("unexpected: %q", got)
	}
	if got := util.MemberIDDisplay(7); got != "#7" {
		t.Fatalf("unexpected: %q", got)
	}
}
