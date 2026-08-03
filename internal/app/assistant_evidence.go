package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var errAssistantEvidenceCursor = errors.New("assistant evidence cursor is invalid")

type assistantEvidenceCursor struct {
	Version        int    `json:"v"`
	UserID         string `json:"u"`
	ConversationID string `json:"c"`
	Tool           string `json:"t"`
	Target         string `json:"g"`
	QueryDigest    string `json:"q"`
	Page           string `json:"p,omitempty"`
	Offset         int    `json:"o,omitempty"`
	ExpiresAt      int64  `json:"e"`
}

func encodeAssistantEvidenceCursor(key [32]byte, cursor assistantEvidenceCursor) (string, error) {
	cursor.Version = 1
	body, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key[:])
	_, _ = mac.Write(body)
	return base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func decodeAssistantEvidenceCursor(key [32]byte, value string, expected assistantEvidenceCursor, now time.Time) (assistantEvidenceCursor, error) {
	bodyText, signatureText, found := strings.Cut(strings.TrimSpace(value), ".")
	if !found || len(value) > 2048 {
		return assistantEvidenceCursor{}, errAssistantEvidenceCursor
	}
	body, err := base64.RawURLEncoding.DecodeString(bodyText)
	if err != nil {
		return assistantEvidenceCursor{}, errAssistantEvidenceCursor
	}
	signature, err := base64.RawURLEncoding.DecodeString(signatureText)
	if err != nil {
		return assistantEvidenceCursor{}, errAssistantEvidenceCursor
	}
	mac := hmac.New(sha256.New, key[:])
	_, _ = mac.Write(body)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return assistantEvidenceCursor{}, errAssistantEvidenceCursor
	}
	var cursor assistantEvidenceCursor
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&cursor) != nil || cursor.Version != 1 || cursor.ExpiresAt <= now.Unix() || cursor.Offset < 0 ||
		cursor.UserID != expected.UserID || cursor.ConversationID != expected.ConversationID || cursor.Tool != expected.Tool ||
		cursor.Target != expected.Target || cursor.QueryDigest != expected.QueryDigest {
		return assistantEvidenceCursor{}, errAssistantEvidenceCursor
	}
	return cursor, nil
}

func assistantEvidenceQueryDigest(query string) string {
	digest := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(query))))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}
