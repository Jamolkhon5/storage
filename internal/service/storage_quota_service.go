package service

import (
	"context"
	"fmt"
	"synxrondrive/internal/domain"
	"synxrondrive/internal/repository"
)

type StorageQuotaService struct {
	quotaRepo *repository.StorageQuotaRepository
}

func NewStorageQuotaService(quotaRepo *repository.StorageQuotaRepository) *StorageQuotaService {
	return &StorageQuotaService{
		quotaRepo: quotaRepo,
	}
}

func (s *StorageQuotaService) GetQuotaInfo(ctx context.Context, ownerID string) (*domain.QuotaInfo, error) {
	quota, err := s.quotaRepo.GetQuota(ctx, ownerID)
	if err != nil {
		return nil, fmt.Errorf("failed to get quota: %w", err)
	}

	availableSpace := quota.TotalBytesLimit - quota.UsedBytes
	usagePercent := float64(quota.UsedBytes) / float64(quota.TotalBytesLimit) * 100

	return &domain.QuotaInfo{
		TotalSpace:     quota.TotalBytesLimit,
		UsedSpace:      quota.UsedBytes,
		AvailableSpace: availableSpace,
		UsagePercent:   usagePercent,
	}, nil
}

func (s *StorageQuotaService) CheckSpaceAvailable(ctx context.Context, ownerID string, requiredBytes int64) (bool, error) {
	quota, err := s.quotaRepo.GetQuota(ctx, ownerID)
	if err != nil {
		return false, fmt.Errorf("failed to get quota: %w", err)
	}

	return (quota.UsedBytes + requiredBytes) <= quota.TotalBytesLimit, nil
}

func (s *StorageQuotaService) UpdateUsedSpace(ctx context.Context, ownerID string) error {
	return s.quotaRepo.CalculateAndUpdateUsedSpace(ctx, ownerID)
}

func (s *StorageQuotaService) UpdateQuotaLimit(ctx context.Context, ownerID string, newLimit int64) error {
	if newLimit < 0 {
		return fmt.Errorf("new quota limit cannot be negative")
	}
	return s.quotaRepo.UpdateQuotaLimit(ctx, ownerID, newLimit)
}
