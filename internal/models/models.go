package models

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"inst/internal/logging"
)

type Comment struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	Text      string    `json:"text"`
	Timestamp time.Time `json:"timestamp"`
	PostCode  string    `json:"post_code"`
	PostURL   string    `json:"post_url"`
}

type RawComment struct {
	Username    string `json:"username"`
	Text        string `json:"text"`
	ProfileLink string `json:"profileLink"`
	CommentLink string `json:"commentLink"`
	Timestamp   int64  `json:"timestamp"`
}

func GenerateCommentID(postCode, username, text string, timestamp int64) string {
	data := fmt.Sprintf("%s|%s|%s|%d", postCode, username, text, timestamp)
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}

func ExtractPostCode(postURL string) string {
	if logging.ShouldLog(logging.DEBUG) {
		logging.DebugLogger.Printf("ExtractPostCode: входной URL=%q", postURL)
	}

	for _, sep := range []string{"/p/", "/reel/"} {
		if idx := strings.Index(postURL, sep); idx != -1 {
			rest := postURL[idx+len(sep):]
			var code string
			if slash := strings.IndexByte(rest, '/'); slash != -1 {
				code = rest[:slash]
			} else {
				code = rest
			}
			if logging.ShouldLog(logging.DEBUG) {
				logging.DebugLogger.Printf("ExtractPostCode: найден разделитель %q, код=%q", sep, code)
			}
			return code
		}
	}
	if logging.ShouldLog(logging.DEBUG) {
		logging.DebugLogger.Printf("ExtractPostCode: не удалось извлечь код из %q", postURL)
	}
	return ""
}
