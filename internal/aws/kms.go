package aws

import (
	"context"
	"errors"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
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
		return nil, err
	}
	if out.KeyMetadata == nil || out.KeyMetadata.KeyId == nil || out.KeyMetadata.Arn == nil {
		return nil, errors.New("kms: CreateKey returned empty metadata")
	}
	return &CreateKMSKeyResult{KeyID: *out.KeyMetadata.KeyId, ARN: *out.KeyMetadata.Arn}, nil
}

// ScheduleKMSKeyDeletion schedules deletion of a KMS key after pendingWindowDays (7..30).
func ScheduleKMSKeyDeletion(ctx context.Context, region, keyID string, pendingWindowDays int32) error {
	if pendingWindowDays == 0 {
		pendingWindowDays = 30
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
	return err
}
