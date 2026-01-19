package aws

import (
    "context"
    "errors"
    "strings"

    awsconfig "github.com/aws/aws-sdk-go-v2/config"
    "github.com/aws/aws-sdk-go-v2/service/kms"
    kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
    "github.com/czarnik/msk-account-cli/internal/logging"
)

type CreateKMSKeyParams struct {
	Region      string
	Description string
	MultiRegion bool
	Tags        map[string]string
}

type CreateKMSKeyResult struct {
	KeyID string
	ARN   string
}

// CreateKMSKey creates a symmetric CMK suitable for encrypt/decrypt and returns its KeyId and ARN.
func CreateKMSKey(ctx context.Context, p CreateKMSKeyParams) (*CreateKMSKeyResult, error) {
    if logging.L != nil {
        logging.L.Info("aws.kms.create_key", "region", p.Region, "multi_region", p.MultiRegion, "tags_count", len(p.Tags))
    }
    cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(p.Region))
	if err != nil {
		return nil, err
	}
	cli := kms.NewFromConfig(cfg)

	in := &kms.CreateKeyInput{
		Description: &p.Description,
		KeyUsage:    kmstypes.KeyUsageTypeEncryptDecrypt,
		KeySpec:     kmstypes.KeySpecSymmetricDefault,
		MultiRegion: &p.MultiRegion,
	}
	if len(p.Tags) > 0 {
		in.Tags = make([]kmstypes.Tag, 0, len(p.Tags))
		for k, v := range p.Tags {
			kk := strings.TrimSpace(k)
			vv := v
			if kk == "" {
				continue
			}
			in.Tags = append(in.Tags, kmstypes.Tag{TagKey: &kk, TagValue: &vv})
		}
	}
    out, err := cli.CreateKey(ctx, in)
    if err != nil {
        if logging.L != nil {
            logging.L.Error("aws.kms.create_key.error", "error", err)
        }
        return nil, err
    }
    if out.KeyMetadata == nil || out.KeyMetadata.KeyId == nil || out.KeyMetadata.Arn == nil {
        return nil, errors.New("kms: CreateKey returned empty metadata")
    }
    res := &CreateKMSKeyResult{KeyID: *out.KeyMetadata.KeyId, ARN: *out.KeyMetadata.Arn}
    if logging.L != nil {
        logging.L.Info("aws.kms.create_key.ok", "key_id", res.KeyID, "arn", res.ARN)
    }
    return res, nil
}

// ScheduleKMSKeyDeletion schedules deletion of a KMS key after pendingWindowDays (7..30).
func ScheduleKMSKeyDeletion(ctx context.Context, region, keyID string, pendingWindowDays int32) error {
	if pendingWindowDays == 0 {
		pendingWindowDays = 30
	}
    if logging.L != nil {
        logging.L.Info("aws.kms.schedule_delete", "region", region, "key_id", keyID, "pending_days", pendingWindowDays)
    }
    cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return err
	}
	cli := kms.NewFromConfig(cfg)
    _, err = cli.ScheduleKeyDeletion(ctx, &kms.ScheduleKeyDeletionInput{
        KeyId:               &keyID,
        PendingWindowInDays: &pendingWindowDays,
    })
    if err != nil {
        if logging.L != nil {
            logging.L.Error("aws.kms.schedule_delete.error", "error", err)
        }
        return err
    }
    if logging.L != nil {
        logging.L.Info("aws.kms.schedule_delete.ok")
    }
    return nil
}
