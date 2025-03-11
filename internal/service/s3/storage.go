// storage.go
package s3

import (
	"context"
	"io"
	"mime/multipart"
)

// S3Object определяет интерфейс для объектов S3
type S3Object interface {
	io.ReadCloser
	ContentLength() int64
	ContentType() string
}

// s3Object реализует интерфейс S3Object
type s3Object struct {
	io.ReadCloser
	contentLength int64
	contentType   string
}

func (o *s3Object) ContentLength() int64 {
	return o.contentLength
}

func (o *s3Object) ContentType() string {
	return o.contentType
}

// Storage определяет интерфейс для работы с S3-совместимым хранилищем
type Storage interface {
	// Существующие методы
	UploadFile(key string, file *multipart.File) error
	UploadBytes(key string, data []byte) error
	GetObject(ctx context.Context, key string) (S3Object, error)
	DeleteObject(key string) error
	GetObjectRange(ctx context.Context, key string, start, end int64) (S3Object, error)
	// Новые методы для поддержки параллельной загрузки
	CreateMultipartUpload(ctx context.Context, key string) (string, error)
	UploadPart(ctx context.Context, uploadID string, key string, partNumber int, data []byte) (string, error)
	CompleteMultipartUpload(ctx context.Context, uploadID string, key string, parts []CompletedPart) error
	AbortMultipartUpload(ctx context.Context, uploadID string, key string) error
}

// CompletedPart представляет загруженную часть файла
type CompletedPart struct {
	PartNumber int
	ETag       string
}
