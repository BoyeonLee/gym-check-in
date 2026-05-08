package util

import (
	"errors"
	"fmt"
	"time"
)

// MaskPhone formats an 11-digit phone string as "010-****-1234" — the prefix
// (3) and last-four are preserved, the middle four digits are replaced with
// asterisks. Kiosk responses MUST go through this helper; admin responses use
// the raw 11-digit string.
//
// The input is expected to satisfy the DB CHECK `^[0-9]{11}$`. Anything else
// is rejected so a corrupt row can't slip past masking.
func MaskPhone(phone string) (string, error) {
	if len(phone) != 11 {
		return "", errors.New("util: phone must be 11 digits")
	}
	for _, r := range phone {
		if r < '0' || r > '9' {
			return "", errors.New("util: phone must be digits only")
		}
	}
	return phone[:3] + "-****-" + phone[7:], nil
}

// MaskBirthMD formats a date as "**-MM-DD" — the year is hidden so a kiosk
// onlooker cannot derive age. Pass any time.Time; only month/day are read.
func MaskBirthMD(d time.Time) string {
	return fmt.Sprintf("**-%02d-%02d", int(d.Month()), d.Day())
}

// MemberIDDisplay formats a numeric id as "#1234". The number is the literal
// member id; we don't pad or zero-extend so very small/very large ids render
// naturally.
func MemberIDDisplay(id int64) string {
	return fmt.Sprintf("#%d", id)
}
