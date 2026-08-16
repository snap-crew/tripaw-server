package image

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

const MaxBytes = 10 << 20

const PathPrefix = "/api/images/"

type Image struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	ContentType string
	Data        []byte
	ByteSize    int
	CreatedAt   time.Time
}

var (
	ErrNotFound    = errors.New("이미지를 찾을 수 없습니다")
	ErrEmpty       = errors.New("이미지 본문이 비어 있습니다")
	ErrTooLarge    = errors.New("이미지는 10MB 이하여야 합니다")
	ErrUnsupported = errors.New("jpeg, png, webp 만 올릴 수 있습니다")
)

var allowed = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/webp": true,
}

func detectContentType(data []byte) (string, error) {
	sniffed := http.DetectContentType(data)
	if i := strings.IndexByte(sniffed, ';'); i >= 0 {
		sniffed = sniffed[:i]
	}
	sniffed = strings.TrimSpace(sniffed)

	if !allowed[sniffed] {
		return "", ErrUnsupported
	}
	return sniffed, nil
}

func URLFor(id uuid.UUID) string {
	return PathPrefix + id.String()
}

func IsManagedURL(raw string) bool {
	rest, ok := strings.CutPrefix(raw, PathPrefix)
	if !ok {
		return false
	}
	_, err := uuid.Parse(rest)
	return err == nil
}
