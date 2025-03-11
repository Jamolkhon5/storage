package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

const (
	defaultTimeout   = 30 * time.Second
	uploadTimeout    = 10 * time.Minute
	downloadTimeout  = 10 * time.Minute
	defaultChunkSize = 5 * 1024 * 1024 // 5MB
)

// Client предоставляет методы для работы с S3-совместимым хранилищем
type Client struct {
	client *s3.Client
	bucket string
}

// NewClient создает новый экземпляр клиента S3
func NewClient(conf *Config) (*Client, error) {
	if conf == nil {
		return nil, fmt.Errorf("configuration is required")
	}

	if conf.AccessKeyID == "" || conf.SecretAccessKey == "" || conf.Bucket == "" {
		return nil, fmt.Errorf("missing required configuration: accessKeyID, secretAccessKey, and bucket are required")
	}

	// Создаем конфигурацию AWS
	creds := aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(
		conf.AccessKeyID,
		conf.SecretAccessKey,
		"",
	))

	// Создаем клиента с кастомными настройками
	client := s3.New(s3.Options{
		BaseEndpoint:     aws.String("https://storage.yandexcloud.net"),
		Region:           "ru-central1",
		Credentials:      creds,
		RetryMode:        aws.RetryModeAdaptive,
		RetryMaxAttempts: 3,
	})

	s3Client := &Client{
		client: client,
		bucket: conf.Bucket,
	}

	// Проверяем подключение к бакету
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	_, err := s3Client.client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(conf.Bucket),
	})
	if err != nil {
		return nil, fmt.Errorf("unable to access bucket %s: %w", conf.Bucket, err)
	}

	return s3Client, nil
}

// UploadFile загружает файл в S3
func (h *Client) UploadFile(key string, file *multipart.File) error {
	if key == "" || file == nil {
		return fmt.Errorf("key and file are required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), uploadTimeout)
	defer cancel()

	// Читаем файл в буфер
	buf := bytes.NewBuffer(make([]byte, 0, defaultChunkSize))
	if _, err := io.Copy(buf, *file); err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	// Загружаем файл в S3
	_, err := h.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(h.bucket),
		Key:    aws.String(key),
		Body:   bytes.NewReader(buf.Bytes()),
	})
	if err != nil {
		return fmt.Errorf("failed to upload file to S3: %w", err)
	}

	return nil
}

// GetObject получает объект из S3
func (h *Client) GetObject(ctx context.Context, key string) (S3Object, error) {
	input := &s3.GetObjectInput{
		Bucket: aws.String(h.bucket),
		Key:    aws.String(key),
	}

	result, err := h.client.GetObject(ctx, input)
	if err != nil {
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			return nil, fmt.Errorf("object not found: %s", key)
		}
		return nil, fmt.Errorf("failed to get object from S3: %w", err)
	}

	return &s3Object{
		ReadCloser:    result.Body,
		contentLength: *result.ContentLength,
		contentType:   *result.ContentType,
	}, nil
}

// GetObjectRange получает часть объекта из S3
func (h *Client) GetObjectRange(ctx context.Context, key string, start, end int64) (S3Object, error) {
	log.Printf("[S3] Starting streaming request for key: %s (range: %d-%d)", key, start, end)
	startTime := time.Now()

	rangeHeader := fmt.Sprintf("bytes=%d-%d", start, end)
	input := &s3.GetObjectInput{
		Bucket: aws.String(h.bucket),
		Key:    aws.String(key),
		Range:  aws.String(rangeHeader),
	}

	// Добавляем трейсинг для запроса к S3
	log.Printf("[S3] Sending request to S3 with range: %s", rangeHeader)
	result, err := h.client.GetObject(ctx, input)
	if err != nil {
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			log.Printf("[S3] Object not found: %s", key)
			return nil, fmt.Errorf("object not found: %s", key)
		}
		log.Printf("[S3] Error getting object range: %v", err)
		return nil, fmt.Errorf("failed to get object range from S3: %w", err)
	}

	elapsed := time.Since(startTime)
	log.Printf("[S3] Stream started successfully. Time to first byte: %v", elapsed)

	return &s3Object{
		ReadCloser:    result.Body,
		contentLength: *result.ContentLength,
		contentType:   *result.ContentType,
	}, nil
}

// DeleteObject удаляет объект из S3
func (h *Client) DeleteObject(key string) error {
	if key == "" {
		return fmt.Errorf("key is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	// Проверяем существование объекта перед удалением
	_, err := h.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(h.bucket),
		Key:    aws.String(key),
	})

	// Если объект не существует, считаем операцию успешной
	var nsk *types.NoSuchKey
	if err != nil && errors.As(err, &nsk) {
		return nil
	}

	// Если возникла другая ошибка при проверке, возвращаем её
	if err != nil {
		return fmt.Errorf("failed to check object existence: %w", err)
	}

	// Если объект существует, удаляем его
	_, err = h.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(h.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("failed to delete object from S3: %w", err)
	}

	return nil
}

// UploadBytes загружает байты в S3
func (h *Client) UploadBytes(key string, data []byte) error {
	if key == "" {
		return fmt.Errorf("key is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), uploadTimeout)
	defer cancel()

	// Загружаем файл в S3
	_, err := h.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(h.bucket),
		Key:    aws.String(key),
		Body:   bytes.NewReader(data),
	})
	if err != nil {
		return fmt.Errorf("failed to upload data to S3: %w", err)
	}

	return nil
}

// CreateMultipartUpload инициализирует загрузку по частям
func (h *Client) CreateMultipartUpload(ctx context.Context, key string) (string, error) {
	input := &s3.CreateMultipartUploadInput{
		Bucket: aws.String(h.bucket),
		Key:    aws.String(key),
	}

	result, err := h.client.CreateMultipartUpload(ctx, input)
	if err != nil {
		return "", fmt.Errorf("failed to create multipart upload: %w", err)
	}

	return *result.UploadId, nil
}

// UploadPart загружает часть файла
func (h *Client) UploadPart(ctx context.Context, uploadID string, key string, partNumber int, data []byte) (string, error) {
	input := &s3.UploadPartInput{
		Bucket:     aws.String(h.bucket),
		Key:        aws.String(key),
		PartNumber: aws.Int32(int32(partNumber)),
		UploadId:   aws.String(uploadID),
		Body:       bytes.NewReader(data),
	}

	result, err := h.client.UploadPart(ctx, input)
	if err != nil {
		return "", fmt.Errorf("failed to upload part %d: %w", partNumber, err)
	}

	return *result.ETag, nil
}

// CompleteMultipartUpload завершает загрузку по частям
func (h *Client) CompleteMultipartUpload(ctx context.Context, uploadID string, key string, parts []CompletedPart) error {
	var completedParts []types.CompletedPart
	for _, part := range parts {
		completedParts = append(completedParts, types.CompletedPart{
			ETag:       aws.String(part.ETag),
			PartNumber: aws.Int32(int32(part.PartNumber)),
		})
	}

	input := &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(h.bucket),
		Key:      aws.String(key),
		UploadId: aws.String(uploadID),
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: completedParts,
		},
	}

	_, err := h.client.CompleteMultipartUpload(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to complete multipart upload: %w", err)
	}

	return nil
}

// AbortMultipartUpload отменяет загрузку по частям
func (h *Client) AbortMultipartUpload(ctx context.Context, uploadID string, key string) error {
	input := &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(h.bucket),
		Key:      aws.String(key),
		UploadId: aws.String(uploadID),
	}

	_, err := h.client.AbortMultipartUpload(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to abort multipart upload: %w", err)
	}

	return nil
}
