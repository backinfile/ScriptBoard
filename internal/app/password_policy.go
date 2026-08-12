package app

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

const minimumPasswordRunes = 15

var blockedPasswords = map[string]struct{}{
	"123456789012345": {}, "adminadminadmin": {}, "administrator": {},
	"changemechangeme": {}, "correcthorsebatterystaple": {}, "iloveyouiloveyou": {},
	"letmeinletmein": {}, "passwordpassword": {}, "password123456": {},
	"qwertyqwertyqwerty": {}, "scriptboardscriptboard": {}, "welcome123456789": {},
}

func validatePasswordPolicy(password, username string) error {
	if !utf8.ValidString(password) || len([]byte(password)) > maxPasswordBytes || utf8.RuneCountInString(password) < minimumPasswordRunes {
		return errors.New("password does not meet length and encoding policy")
	}
	normalized := strings.ToLower(strings.TrimSpace(password))
	if normalized == "" || normalized == strings.ToLower(strings.TrimSpace(username)) {
		return errors.New("password matches account context")
	}
	if _, blocked := blockedPasswords[normalized]; blocked {
		return errors.New("password appears in the local blocklist")
	}
	var first rune
	allSame := true
	for index, value := range normalized {
		if unicode.IsControl(value) {
			return errors.New("password contains a control character")
		}
		if index == 0 {
			first = value
		} else if value != first {
			allSame = false
		}
	}
	if allSame {
		return errors.New("password is a repeated character")
	}
	return nil
}
