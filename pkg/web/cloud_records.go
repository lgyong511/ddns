package web

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"ddns/pkg/config"
	"ddns/pkg/provider"
	"ddns/pkg/utils"
)

func deleteCloudRecordsWithRetry(ctx context.Context, operator CloudOperator, record config.Record, retries int, interval time.Duration) error {
	var err error
	for attempt := 0; attempt <= retries; attempt++ {
		if err = ctx.Err(); err != nil {
			return err
		}
		if _, err = deleteCloudRecords(ctx, operator, record); err == nil {
			return nil
		}
		if attempt == retries {
			break
		}
		slog.Warn("云端删除失败，准备重试", "attempt", attempt+1, "maxRetries", retries, "err", err)
		select {
		case <-time.After(interval):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return fmt.Errorf("云端删除重试 %d 次后仍然失败: %w", retries, err)
}

func deleteCloudRecords(ctx context.Context, operator CloudOperator, record config.Record) ([]provider.Record, error) {
	type deletion struct {
		subDomain string
		record    provider.Record
	}
	deletions := make([]deletion, 0)
	deleted := make([]provider.Record, 0)
	for _, subDomain := range record.SubDomains {
		rr, domain, err := utils.ParseDomain(subDomain)
		if err != nil {
			return nil, fmt.Errorf("解析云端记录 %q 失败: %w", subDomain, err)
		}
		cloudRecords, err := operator.GetSub(ctx, subDomain, record.IPVersion)
		if errors.Is(err, provider.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("查询云端记录 %q 失败: %w", subDomain, err)
		}
		matches := make([]provider.Record, 0, 1)
		for _, cloudRecord := range cloudRecords {
			if sameCloudRecord(cloudRecord, rr, domain, record.IPVersion.RecordType()) {
				matches = append(matches, cloudRecord)
			}
		}
		if len(matches) > 1 {
			return nil, fmt.Errorf("云端记录 %q 存在 %d 条同名同类型记录，无法安全删除", subDomain, len(matches))
		}
		if len(matches) == 1 {
			deletions = append(deletions, deletion{subDomain: subDomain, record: matches[0]})
		}
	}
	for _, item := range deletions {
		if err := operator.Delete(ctx, item.record.RecordId, item.record.DomainName); err != nil {
			if rollbackErr := restoreCloudRecords(ctx, operator, deleted); rollbackErr != nil {
				return nil, fmt.Errorf("删除云端记录 %q 失败: %w（回滚失败: %v）", item.subDomain, err, rollbackErr)
			}
			return nil, fmt.Errorf("删除云端记录 %q 失败: %w", item.subDomain, err)
		}
		deleted = append(deleted, item.record)
	}
	return deleted, nil
}

func restoreCloudRecords(ctx context.Context, operator CloudOperator, records []provider.Record) error {
	var restoreErr error
	for i := len(records) - 1; i >= 0; i-- {
		record := records[i]
		record.RecordId = ""
		if _, err := operator.Create(ctx, &record); err != nil {
			restoreErr = errors.Join(restoreErr, fmt.Errorf("恢复云端记录失败: %w", err))
		}
	}
	return restoreErr
}

func sameCloudRecord(record provider.Record, rr, domain, recordType string) bool {
	return strings.EqualFold(strings.TrimSuffix(record.RR, "."), strings.TrimSuffix(rr, ".")) &&
		strings.EqualFold(strings.TrimSuffix(record.DomainName, "."), strings.TrimSuffix(domain, ".")) &&
		record.Type == recordType
}
