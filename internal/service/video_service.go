package service

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/xfrr/goffmpeg/transcoder"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
)

type VideoService struct {
	fileService *FileService
	outputDir   string
}

func NewVideoService(fileService *FileService, outputDir string) (*VideoService, error) {
	// Проверяем наличие ffmpeg
	_, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("ffmpeg not found: %w", err)
	}

	// Создаем директорию, если её нет
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output directory: %w", err)
	}

	return &VideoService{
		fileService: fileService,
		outputDir:   outputDir,
	}, nil
}

func (s *VideoService) PrepareStreamingVideo(ctx context.Context, fileUUID uuid.UUID, userID string) (string, error) {
	log.Printf("[VideoService] Starting video preparation for UUID: %s", fileUUID)

	outputPath := filepath.Join(s.outputDir, fileUUID.String())
	playlistPath := filepath.Join(outputPath, "playlist.m3u8")

	if _, err := os.Stat(playlistPath); err == nil {
		log.Printf("[VideoService] Found existing playlist for UUID: %s", fileUUID)
		return playlistPath, nil
	}

	if err := os.MkdirAll(outputPath, 0755); err != nil {
		log.Printf("[VideoService] Failed to create directory: %v", err)
		return "", fmt.Errorf("failed to create output directory: %w", err)
	}

	reader, err := s.fileService.GetFileData(ctx, fileUUID, userID)
	if err != nil {
		log.Printf("[VideoService] Failed to get file data: %v", err)
		return "", err
	}
	if closer, ok := reader.(io.Closer); ok {
		defer closer.Close()
	}

	inputFile, err := os.CreateTemp(os.TempDir(), "input-*.mp4")
	if err != nil {
		log.Printf("[VideoService] Failed to create temp file: %v", err)
		return "", err
	}
	defer os.Remove(inputFile.Name())

	// Создаем канал для отслеживания завершения копирования
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(inputFile, reader)
		done <- err
	}()

	// Ждем завершения копирования или отмены контекста
	select {
	case err := <-done:
		if err != nil {
			log.Printf("[VideoService] Failed to copy video data: %v", err)
			return "", fmt.Errorf("failed to copy video data: %w", err)
		}
	case <-ctx.Done():
		log.Printf("[VideoService] Context canceled while copying file")
		return "", ctx.Err()
	}

	// Закрываем файл после записи
	if err := inputFile.Close(); err != nil {
		log.Printf("[VideoService] Failed to close input file: %v", err)
		return "", err
	}

	trans := new(transcoder.Transcoder)

	log.Printf("[VideoService] Initializing transcoder for UUID: %s", fileUUID)
	err = trans.Initialize(inputFile.Name(), playlistPath)
	if err != nil {
		log.Printf("[VideoService] Failed to initialize transcoder: %v", err)
		return "", err
	}

	trans.MediaFile().SetVideoCodec("libx264")
	trans.MediaFile().SetAudioCodec("aac")
	trans.MediaFile().SetHlsSegmentDuration(4)
	trans.MediaFile().SetHlsPlaylistType("vod")
	trans.MediaFile().SetHlsSegmentFilename(filepath.Join(outputPath, "segment_%d.ts"))

	// Запускаем транскодирование с обработкой отмены контекста
	doneTrans := trans.Run(true)
	select {
	case err := <-doneTrans:
		if err != nil {
			log.Printf("[VideoService] Transcoding failed: %v", err)
			return "", fmt.Errorf("transcoding failed: %w", err)
		}
	case <-ctx.Done():
		log.Printf("[VideoService] Context canceled while transcoding")
		return "", ctx.Err()
	}

	log.Printf("[VideoService] Successfully prepared video for UUID: %s", fileUUID)
	return playlistPath, nil
}
