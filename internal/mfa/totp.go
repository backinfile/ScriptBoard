package mfa

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"
)

const totpPeriod = 30 * time.Second

func TOTPCode(secret string, at time.Time) (string, error) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil || len(key) < 10 {
		return "", errors.New("TOTP secret is invalid")
	}
	step := uint64(at.Unix() / int64(totpPeriod/time.Second))
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], step)
	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(counter[:])
	digest := mac.Sum(nil)
	offset := digest[len(digest)-1] & 0x0f
	value := binary.BigEndian.Uint32(digest[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", value%1_000_000), nil
}

func validTOTP(secret, candidate string, now time.Time, lastStep int64) (int64, bool) {
	if len(candidate) != 6 {
		return 0, false
	}
	for _, value := range candidate {
		if value < '0' || value > '9' {
			return 0, false
		}
	}
	current := now.Unix() / int64(totpPeriod/time.Second)
	for _, step := range []int64{current - 1, current, current + 1} {
		if step <= lastStep || step < 0 {
			continue
		}
		code, err := TOTPCode(secret, time.Unix(step*int64(totpPeriod/time.Second), 0))
		if err == nil && hmac.Equal([]byte(code), []byte(candidate)) {
			return step, true
		}
	}
	return 0, false
}
