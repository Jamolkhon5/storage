package handler

import (
	"context"
	"log"
	"synxrondrive/internal/domain"
	"synxrondrive/internal/service"
	pb "synxrondrive/pkg/proto/recording_v1"
)

type RecordingHandler struct {
	pb.UnimplementedRecordingServiceServer
	recordingService *service.RecordingService
}

func NewRecordingHandler(recordingService *service.RecordingService) *RecordingHandler {
	return &RecordingHandler{
		recordingService: recordingService,
	}
}

func (h *RecordingHandler) SaveRecording(ctx context.Context, req *pb.SaveRecordingRequest) (*pb.SaveRecordingResponse, error) {
	log.Printf("[FileManager] Получен GRPC запрос на сохранение записи:")
	log.Printf("[FileManager] - RecordingID: %s", req.RecordingId)
	log.Printf("[FileManager] - UserID: %s", req.UserId)
	log.Printf("[FileManager] - FileName: %s", req.FileName)
	log.Printf("[FileManager] - FilePath: %s", req.FilePath)
	log.Printf("[FileManager] - Size: %d bytes", req.SizeBytes)
	log.Printf("[FileManager] - MIME: %s", req.MimeType)

	resp, err := h.recordingService.SaveRecording(ctx, req)
	if err != nil {
		log.Printf("[FileManager] Ошибка сохранения записи: %v", err)
		return nil, err
	}

	log.Printf("[FileManager] Запись успешно сохранена:")
	log.Printf("[FileManager] - FileID: %s", resp.FileId)
	log.Printf("[FileManager] - FolderID: %s", resp.FolderId)

	log.Printf("[FileManager] Запись успешно сохранена в папке: %s (ID: %s)",
		domain.RecordingsFolderName, resp.FolderId)

	return resp, nil
}
