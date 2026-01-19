package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/czarnik/msk-account-cli/internal/config"
	"github.com/czarnik/msk-account-cli/internal/logging"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type CreateSecretParams struct {
	Region     string
	SecretName string
	KMSKeyID   string
	Username   string
	Password   string
	Tags       map[string]string
}

// CreateSecret creates the SCRAM credential secret and returns its ARN.
func CreateSecret(ctx context.Context, p CreateSecretParams) (string, error) {
	tctx, span := otel.Tracer("github.com/czarnik/msk-account-cli/aws").Start(ctx, "secrets.create",
		trace.WithAttributes(
			attribute.String("aws.region", p.Region),
			attribute.String("secret.name", p.SecretName),
		),
	)
	defer span.End()
	_ = tctx
	v := config.Validator{}
	if err := v.ValidateSecretName(p.SecretName); err != nil {
		return "", err
	}
	if err := v.ValidateKMSKeyID(p.KMSKeyID); err != nil {
		return "", err
	}
	if err := v.ValidateSecretPayload(p.Username, p.Password); err != nil {
		return "", err
	}

	if logging.L != nil {
		logging.L.Info("aws.secrets.create",
			"region", p.Region,
			"secret_name", p.SecretName,
			"kms_key_id", p.KMSKeyID,
			"username", p.Username,
			"tags_count", len(p.Tags),
		)
	}

	cfg, err := awsconfig.LoadDefaultConfig(tctx, awsconfig.WithRegion(p.Region))
	if err != nil {
		return "", err
	}
	cli := secretsmanager.NewFromConfig(cfg)

	payload, err := json.Marshal(struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}{Username: p.Username, Password: p.Password})
	if err != nil {
		return "", err
	}

	var tags []smtypes.Tag
	for k, v := range p.Tags {
		if strings.TrimSpace(k) == "" {
			continue
		}
		vv := v
		kk := k
		tags = append(tags, smtypes.Tag{Key: &kk, Value: &vv})
	}

	// Guardrail: ensure KMS key id present and not alias handled above
	in := &secretsmanager.CreateSecretInput{
		Name:         &p.SecretName,
		KmsKeyId:     &p.KMSKeyID,
		SecretString: awsString(string(payload)),
		Tags:         tags,
	}

	out, err := cli.CreateSecret(tctx, in)
	if err != nil {
		if logging.L != nil {
			logging.L.Error("aws.secrets.create.error", "error", err)
		}
		return "", err
	}
	if out.ARN == nil {
		return "", errors.New("CreateSecret returned nil ARN")
	}
	if logging.L != nil {
		logging.L.Info("aws.secrets.create.ok", "arn", *out.ARN)
	}
	return *out.ARN, nil
}

type GetSecretParams struct {
	Region     string
	SecretARN  string
	SecretName string
}

type GetSecretResult struct {
	Username string
	Password string
}

func GetSecret(ctx context.Context, p GetSecretParams) (*GetSecretResult, error) {
	tctx, span := otel.Tracer("github.com/czarnik/msk-account-cli/aws").Start(ctx, "secrets.get",
		trace.WithAttributes(
			attribute.String("aws.region", p.Region),
			attribute.String("secret.selector", func() string {
				if p.SecretARN != "" {
					return "arn"
				}
				return "name"
			}()),
		),
	)
	defer span.End()
	_ = tctx
	if p.SecretARN == "" && p.SecretName == "" {
		return nil, errors.New("either SecretARN or SecretName must be provided")
	}
	if logging.L != nil {
		id := p.SecretARN
		kind := "arn"
		if id == "" {
			id = p.SecretName
			kind = "name"
		}
		logging.L.Info("aws.secrets.get", "region", p.Region, kind, id)
	}

	cfg, err := awsconfig.LoadDefaultConfig(tctx, awsconfig.WithRegion(p.Region))
	if err != nil {
		return nil, err
	}
	cli := secretsmanager.NewFromConfig(cfg)

	id := p.SecretARN
	if id == "" {
		id = p.SecretName
	}
	ctx2, cancel := context.WithTimeout(tctx, 30*time.Second)
	defer cancel()
	out, err := cli.GetSecretValue(ctx2, &secretsmanager.GetSecretValueInput{SecretId: &id})
	if err != nil {
		if logging.L != nil {
			logging.L.Error("aws.secrets.get.error", "error", err)
		}
		return nil, err
	}
	if out.SecretString == nil {
		return nil, errors.New("secret has no SecretString")
	}
	var payload struct{ Username, Password string }
	if err := json.Unmarshal([]byte(*out.SecretString), &payload); err != nil {
		return nil, fmt.Errorf("failed to parse secret JSON: %w", err)
	}
	if logging.L != nil {
		logging.L.Info("aws.secrets.get.ok", "username", payload.Username)
	}
	return &GetSecretResult{Username: payload.Username, Password: payload.Password}, nil
}

func awsString(s string) *string { return &s }

// DescribeSecret to retrieve metadata, including KMS key id
type DescribeSecretParams struct {
	Region string
	Id     string // name or ARN
}

type DescribeSecretResult struct {
	ARN      string
	KMSKeyID string
}

func DescribeSecret(ctx context.Context, p DescribeSecretParams) (*DescribeSecretResult, error) {
	tctx, span := otel.Tracer("github.com/czarnik/msk-account-cli/aws").Start(ctx, "secrets.describe",
		trace.WithAttributes(
			attribute.String("aws.region", p.Region),
			attribute.String("secret.id", p.Id),
		),
	)
	defer span.End()
	_ = tctx
	if logging.L != nil {
		logging.L.Info("aws.secrets.describe", "region", p.Region, "id", p.Id)
	}
	cfg, err := awsconfig.LoadDefaultConfig(tctx, awsconfig.WithRegion(p.Region))
	if err != nil {
		return nil, err
	}
	cli := secretsmanager.NewFromConfig(cfg)
	out, err := cli.DescribeSecret(tctx, &secretsmanager.DescribeSecretInput{SecretId: &p.Id})
	if err != nil {
		if logging.L != nil {
			logging.L.Error("aws.secrets.describe.error", "error", err)
		}
		return nil, err
	}
	res := &DescribeSecretResult{}
	if out.ARN != nil {
		res.ARN = *out.ARN
	}
	if out.KmsKeyId != nil {
		res.KMSKeyID = *out.KmsKeyId
	}
	if logging.L != nil {
		logging.L.Info("aws.secrets.describe.ok", "arn", res.ARN, "kms_key_id", res.KMSKeyID)
	}
	return res, nil
}

// DeleteSecret deletes the secret; either force delete or recovery window.
type DeleteSecretParams struct {
	Region             string
	Id                 string // name or ARN
	Force              bool
	RecoveryWindowDays int32 // 7..30 if not Force
}

func DeleteSecret(ctx context.Context, p DeleteSecretParams) error {
	tctx, span := otel.Tracer("github.com/czarnik/msk-account-cli/aws").Start(ctx, "secrets.delete",
		trace.WithAttributes(
			attribute.String("aws.region", p.Region),
			attribute.String("secret.id", p.Id),
		),
	)
	defer span.End()
	_ = tctx
	if logging.L != nil {
		logging.L.Info("aws.secrets.delete", "region", p.Region, "id", p.Id, "force", p.Force, "recovery_days", p.RecoveryWindowDays)
	}
	cfg, err := awsconfig.LoadDefaultConfig(tctx, awsconfig.WithRegion(p.Region))
	if err != nil {
		return err
	}
	cli := secretsmanager.NewFromConfig(cfg)
	in := &secretsmanager.DeleteSecretInput{SecretId: &p.Id}
	if p.Force {
		in.ForceDeleteWithoutRecovery = awsBool(true)
	} else {
		// default window if unset
		days := p.RecoveryWindowDays
		if days == 0 {
			days = 30
		}
		in.RecoveryWindowInDays = awsInt64(int64(days))
	}
	_, err = cli.DeleteSecret(tctx, in)
	if err != nil {
		if logging.L != nil {
			logging.L.Error("aws.secrets.delete.error", "error", err)
		}
		return err
	}
	if logging.L != nil {
		logging.L.Info("aws.secrets.delete.ok")
	}
	return nil
}

func awsBool(b bool) *bool    { return &b }
func awsInt64(i int64) *int64 { return &i }

// SecretSummary provides a minimal view for listing secrets.
type SecretSummary struct {
	Name string
	ARN  string
}

// ListMSKSecrets lists secrets with names starting with AmazonMSK_.
func ListMSKSecrets(ctx context.Context, region string) ([]SecretSummary, error) {
	if logging.L != nil {
		logging.L.Info("aws.secrets.list", "region", region)
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, err
	}
	cli := secretsmanager.NewFromConfig(cfg)
	in := &secretsmanager.ListSecretsInput{
		Filters: []smtypes.Filter{{
			Key:    smtypes.FilterNameStringTypeName,
			Values: []string{"AmazonMSK_"},
		}},
	}
	p := secretsmanager.NewListSecretsPaginator(cli, in)
	var outRows []SecretSummary
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, s := range page.SecretList {
			name := awsDeref(s.Name)
			arn := awsDeref(s.ARN)
			if strings.HasPrefix(name, "AmazonMSK_") {
				outRows = append(outRows, SecretSummary{Name: name, ARN: arn})
			}
		}
	}
	if logging.L != nil {
		logging.L.Info("aws.secrets.list.ok", "count", len(outRows))
	}
	return outRows, nil
}

func awsDeref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
