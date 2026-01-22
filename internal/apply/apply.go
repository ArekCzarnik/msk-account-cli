package apply

import (
	"context"
	"errors"
	"os"
	"strings"

	iaws "github.com/czarnik/msk-account-cli/internal/aws"
	"github.com/czarnik/msk-account-cli/internal/config"
	"github.com/czarnik/msk-account-cli/internal/kafka"
	"github.com/czarnik/msk-account-cli/internal/logging"
)

type Result struct {
	SecretARN      string
	ACLsCreated    int
	Associated     bool
	KMSKeyCreated  bool
	KMSKeyARN      string
	PreviewActions []Action // present when DryRun
	TopicsCreated  int
}

// Options controls how Apply behaves.
type Options struct {
	DryRun          bool
	RollbackOnError bool
}

// Action describes a planned step (used in dry-run output).
type Action struct {
	Type    string            `json:"type"`
	Details map[string]string `json:"details,omitempty"`
}

// Apply executes the plan end-to-end.
func Apply(ctx context.Context, p *Plan, opt *Options) (*Result, error) {
	if p == nil {
		return nil, errors.New("nil plan")
	}
	if opt == nil {
		opt = &Options{}
	}

	// Resolve region
	region := strings.TrimSpace(p.AWS.Region)
	if region == "" {
		region = strings.TrimSpace(config.GetDefaultRegion())
	}
	if region == "" {
		return nil, errors.New("aws.region is required (plan or default config)")
	}

	// Resolve KMS key for the secret
	var kmsKeyID string
	if p.Account.KMS.UseExistingKey {
		kmsKeyID = strings.TrimSpace(p.Account.KMS.KMSKeyID)
		if kmsKeyID == "" {
			return nil, errors.New("account.kms.kms_key_id must be set when use_existing_key=true")
		}
	} else if p.Account.KMS.Create {
		if opt.DryRun {
			// simulate
			kmsKeyID = "(to-be-created KMS key ARN)"
		} else {
			res, err := iaws.CreateKMSKey(ctx, iaws.CreateKMSKeyParams{
				Region:      region,
				Description: p.Account.KMS.Description,
				MultiRegion: p.Account.KMS.MultiRegion,
				Tags:        p.Account.KMS.Tags,
			})
			if err != nil {
				return nil, err
			}
			kmsKeyID = res.ARN
		}
	} else {
		kmsKeyID = strings.TrimSpace(config.GetDefaultKMSKeyID())
		if kmsKeyID == "" {
			return nil, errors.New("no KMS key configured: set account.kms.use_existing_key + kms_key_id, or account.kms.create=true, or configure default kms_key_id")
		}
	}

	// Resolve password
	password := strings.TrimSpace(p.Account.Password)
	if password == "" && strings.TrimSpace(p.Account.PasswordFromEnv) != "" {
		password = strings.TrimSpace(strings.TrimSpace(getenv(p.Account.PasswordFromEnv)))
	}
	if password == "" {
		return nil, errors.New("account.password (or password_from_env) must be provided")
	}

	// Prepare result
	res := &Result{}

	// Create secret
	var arn string
	if opt.DryRun {
		arn = "(to-be-created secret arn)"
	} else {
		var err error
		arn, err = iaws.CreateSecret(ctx, iaws.CreateSecretParams{
			Region:     region,
			SecretName: p.Account.SecretName,
			KMSKeyID:   kmsKeyID,
			Username:   p.Account.Username,
			Password:   password,
			Tags:       p.Account.Tags,
		})
		if err != nil {
			// attempt rollback if requested and not a dry-run
			if opt.RollbackOnError {
				_ = Rollback(ctx, p, RollbackOptions{})
			}
			return nil, err
		}
	}
	res.SecretARN = arn

	// Associate secret with cluster if requested
	if p.Account.AssociateWithCluster {
		clusterARN := strings.TrimSpace(p.AWS.ClusterARN)
		if clusterARN == "" {
			return nil, errors.New("aws.cluster_arn is required when account.associate_with_cluster=true")
		}
		if !opt.DryRun {
			if err := iaws.AssociateSecrets(ctx, iaws.AssociateSecretsParams{Region: region, ClusterARN: clusterARN, SecretARNs: []string{arn}}); err != nil {
				if opt.RollbackOnError {
					_ = Rollback(ctx, p, RollbackOptions{})
				}
				return nil, err
			}
			if logging.L != nil {
				logging.L.Info("apply.associate_secret.ok", "cluster_arn", clusterARN, "secret_arn", arn)
			}
		}
		res.Associated = true
	}

	// Create ACLs for the new user
	created := 0
	if len(p.ACLs) > 0 {
		// Build admin auth
		var ac kafka.AuthConfig
		if len(p.AdminConnection.Brokers) > 0 {
			ac.Brokers = append([]string(nil), p.AdminConnection.Brokers...)
		} else {
			ac.Brokers = config.GetDefaultBrokers()
		}
		if len(ac.Brokers) == 0 {
			return nil, errors.New("admin_connection.brokers is required (plan or default config)")
		}

		adminRegion := strings.TrimSpace(p.AdminConnection.Auth.Region)
		if adminRegion == "" {
			adminRegion = region
		}
		if !opt.DryRun {
			if strings.TrimSpace(p.AdminConnection.Auth.SecretARN) != "" {
				sec, err := iaws.GetSecret(ctx, iaws.GetSecretParams{Region: adminRegion, SecretARN: p.AdminConnection.Auth.SecretARN})
				if err != nil {
					return nil, err
				}
				ac.SASLUsername = sec.Username
				ac.SASLPassword = sec.Password
			} else {
				ac.SASLUsername = strings.TrimSpace(p.AdminConnection.Auth.SASLUsername)
				ac.SASLPassword = strings.TrimSpace(p.AdminConnection.Auth.SASLPassword)
			}
		}

		if mech := strings.TrimSpace(p.AdminConnection.Auth.SCRAMMechanism); mech != "" {
			ac.SCRAMMechanism = mech
		} else {
			m := strings.TrimSpace(config.GetDefaultSCRAM())
			if m == "" {
				m = "sha512"
			}
			ac.SCRAMMechanism = m
		}

		if !opt.DryRun {
			if err := ac.Validate(); err != nil {
				return nil, err
			}
		}

		var admin kafkaAdminForApply
		if !opt.DryRun {
			var err error
			admin, err = kafka.NewAdmin(ctx, ac)
			if err != nil {
				if opt.RollbackOnError {
					_ = Rollback(ctx, p, RollbackOptions{})
				}
				return nil, err
			}
			defer admin.Close()
		}

		v := config.Validator{}
		for _, a := range p.ACLs {
			// Defaults and placeholder expansion
			patt := a.ResourcePattern
			if strings.TrimSpace(patt) == "" {
				patt = "literal"
			}
			perm := a.Permission
			if strings.TrimSpace(perm) == "" {
				perm = "allow"
			}
			host := a.Host
			if strings.TrimSpace(host) == "" {
				host = "*"
			}
			principal := p.ExpandPlaceholders(a.Principal)

			// Validate
			if err := v.ValidateResourceType(a.ResourceType); err != nil {
				return nil, err
			}
			if err := v.ValidateResourcePattern(patt); err != nil {
				return nil, err
			}
			if err := v.ValidateOperation(a.Operation); err != nil {
				return nil, err
			}
			if err := v.ValidatePermission(perm); err != nil {
				return nil, err
			}

			if !opt.DryRun {
				if err := admin.CreateACL(ctx, kafka.CreateACLParams{
					ResourceType:    a.ResourceType,
					ResourceName:    a.ResourceName,
					ResourcePattern: patt,
					Principal:       principal,
					Host:            host,
					Operation:       a.Operation,
					Permission:      perm,
				}); err != nil {
					if opt.RollbackOnError {
						_ = Rollback(ctx, p, RollbackOptions{})
					}
					return nil, err
				}
			}
			created++
			if opt.DryRun {
				if res.PreviewActions == nil {
					res.PreviewActions = []Action{}
				}
				res.PreviewActions = append(res.PreviewActions, Action{Type: "create_acl", Details: map[string]string{
					"resource_type":    a.ResourceType,
					"resource_name":    a.ResourceName,
					"resource_pattern": patt,
					"principal":        principal,
					"host":             host,
					"operation":        a.Operation,
					"permission":       perm,
				}})
			}
		}
	}
	res.ACLsCreated = created

	// Topics ensure/create
	topicsCreated := 0
	if len(p.Topics) > 0 {
		// Reuse admin auth
		var ac kafka.AuthConfig
		if len(p.AdminConnection.Brokers) > 0 {
			ac.Brokers = append([]string(nil), p.AdminConnection.Brokers...)
		} else {
			ac.Brokers = config.GetDefaultBrokers()
		}
		if len(ac.Brokers) == 0 {
			return nil, errors.New("admin_connection.brokers is required (plan or default config)")
		}
		adminRegion := strings.TrimSpace(p.AdminConnection.Auth.Region)
		if adminRegion == "" {
			adminRegion = region
		}
		if !opt.DryRun {
			if strings.TrimSpace(p.AdminConnection.Auth.SecretARN) != "" {
				sec, err := iaws.GetSecret(ctx, iaws.GetSecretParams{Region: adminRegion, SecretARN: p.AdminConnection.Auth.SecretARN})
				if err != nil {
					return nil, err
				}
				ac.SASLUsername = sec.Username
				ac.SASLPassword = sec.Password
			} else {
				ac.SASLUsername = strings.TrimSpace(p.AdminConnection.Auth.SASLUsername)
				ac.SASLPassword = strings.TrimSpace(p.AdminConnection.Auth.SASLPassword)
			}
		}
		if mech := strings.TrimSpace(p.AdminConnection.Auth.SCRAMMechanism); mech != "" {
			ac.SCRAMMechanism = mech
		} else {
			m := strings.TrimSpace(config.GetDefaultSCRAM())
			if m == "" {
				m = "sha512"
			}
			ac.SCRAMMechanism = m
		}
		var admin kafkaTopicAdmin
		if !opt.DryRun {
			if err := ac.Validate(); err != nil {
				return nil, err
			}
			var err error
			admin, err = kafka.NewAdmin(ctx, ac)
			if err != nil {
				return nil, err
			}
			defer admin.Close()
		}
		for _, t := range p.Topics {
			if !t.CreateIfMissing {
				continue
			}
			if opt.DryRun {
				if res.PreviewActions == nil {
					res.PreviewActions = []Action{}
				}
				res.PreviewActions = append(res.PreviewActions, Action{Type: "ensure_topic", Details: map[string]string{
					"name": t.Name,
				}})
				topicsCreated++ // as planned potential create
				continue
			}
			createdNow, err := admin.EnsureTopicExists(ctx, kafka.TopicSpec{Name: t.Name, Partitions: t.Partitions, ReplicationFactor: t.ReplicationFactor, Configs: t.Configs})
			if err != nil {
				if opt.RollbackOnError {
					_ = Rollback(ctx, p, RollbackOptions{})
				}
				return nil, err
			}
			if createdNow {
				topicsCreated++
			}
		}
	}
	res.TopicsCreated = topicsCreated

	// mark kms created if applicable
	res.KMSKeyCreated = p.Account.KMS.Create
	if p.Account.KMS.Create {
		res.KMSKeyARN = kmsKeyID
		if opt.DryRun {
			if res.PreviewActions == nil {
				res.PreviewActions = []Action{}
			}
			res.PreviewActions = append(res.PreviewActions, Action{Type: "create_kms_key", Details: map[string]string{
				"description":  p.Account.KMS.Description,
				"multi_region": fmtBool(p.Account.KMS.MultiRegion),
			}})
		}
	}
	if opt.DryRun {
		if res.PreviewActions == nil {
			res.PreviewActions = []Action{}
		}
		res.PreviewActions = append([]Action{
			{Type: "create_secret", Details: map[string]string{
				"secret_name": p.Account.SecretName,
				"username":    p.Account.Username,
			}},
		}, res.PreviewActions...)
		if p.Account.AssociateWithCluster {
			res.PreviewActions = append(res.PreviewActions, Action{Type: "associate_secret", Details: map[string]string{
				"cluster_arn": p.AWS.ClusterARN,
				"secret_name": p.Account.SecretName,
			}})
		}
	}
	return res, nil
}

func getenv(k string) string { return os.Getenv(strings.TrimSpace(k)) }

// kafkaAdminForApply narrows admin surface for easier mocking if needed.
type kafkaAdminForApply interface {
	CreateACL(ctx context.Context, p kafka.CreateACLParams) error
	Close() error
}

type kafkaTopicAdmin interface {
	EnsureTopicExists(ctx context.Context, s kafka.TopicSpec) (bool, error)
	Close() error
}

// -----------------
// Rollback support
// -----------------

type RollbackOptions struct {
	ForceSecretDelete    bool
	KMSPendingWindowDays int32 // default 30
}

type RollbackResult struct {
	ACLsDeleted        int
	Disassociated      bool
	SecretDeleted      bool
	KMSDeleteScheduled bool
}

// Rollback attempts to undo the resources described by the plan.
func Rollback(ctx context.Context, p *Plan, opt RollbackOptions) error {
	// defaults
	if opt.KMSPendingWindowDays == 0 {
		opt.KMSPendingWindowDays = 30
	}

	region := strings.TrimSpace(p.AWS.Region)
	if region == "" {
		region = strings.TrimSpace(config.GetDefaultRegion())
	}
	if region == "" {
		return errors.New("aws.region is required for rollback (plan or default config)")
	}

	// 1) Delete ACLs
	if len(p.ACLs) > 0 {
		var ac kafka.AuthConfig
		if len(p.AdminConnection.Brokers) > 0 {
			ac.Brokers = append([]string(nil), p.AdminConnection.Brokers...)
		} else {
			ac.Brokers = config.GetDefaultBrokers()
		}
		if len(ac.Brokers) == 0 {
			return errors.New("admin_connection.brokers is required for rollback (plan or default config)")
		}
		adminRegion := strings.TrimSpace(p.AdminConnection.Auth.Region)
		if adminRegion == "" {
			adminRegion = region
		}
		if strings.TrimSpace(p.AdminConnection.Auth.SecretARN) != "" {
			sec, err := iaws.GetSecret(ctx, iaws.GetSecretParams{Region: adminRegion, SecretARN: p.AdminConnection.Auth.SecretARN})
			if err != nil {
				return err
			}
			ac.SASLUsername = sec.Username
			ac.SASLPassword = sec.Password
		} else {
			ac.SASLUsername = strings.TrimSpace(p.AdminConnection.Auth.SASLUsername)
			ac.SASLPassword = strings.TrimSpace(p.AdminConnection.Auth.SASLPassword)
		}
		mech := strings.TrimSpace(p.AdminConnection.Auth.SCRAMMechanism)
		if mech == "" {
			mech = strings.TrimSpace(config.GetDefaultSCRAM())
		}
		if mech == "" {
			mech = "sha512"
		}
		ac.SCRAMMechanism = mech
		if err := ac.Validate(); err != nil {
			return err
		}
		admin, err := kafka.NewAdmin(ctx, ac)
		if err != nil {
			return err
		}
		defer admin.Close()
		v := config.Validator{}
		for _, a := range p.ACLs {
			patt := a.ResourcePattern
			if strings.TrimSpace(patt) == "" {
				patt = "literal"
			}
			principal := p.ExpandPlaceholders(a.Principal)
			if err := v.ValidateResourceType(a.ResourceType); err != nil {
				return err
			}
			if err := v.ValidateResourcePattern(patt); err != nil {
				return err
			}
			if err := v.ValidateOperation(a.Operation); err != nil {
				return err
			}
			if err := v.ValidatePermission(func(s string) string {
				if s == "" {
					return "allow"
				}
				return s
			}(a.Permission)); err != nil {
				return err
			}
			if _, err := admin.DeleteACLs(ctx, kafka.DeleteACLsParams{
				ResourceType:    a.ResourceType,
				ResourceName:    a.ResourceName,
				ResourcePattern: patt,
				Principal:       principal,
				Host: func(h string) string {
					if strings.TrimSpace(h) == "" {
						return "*"
					}
					return h
				}(a.Host),
				Operation: a.Operation,
				Permission: func(p string) string {
					if strings.TrimSpace(p) == "" {
						return "allow"
					}
					return p
				}(a.Permission),
			}); err != nil {
				return err
			}
		}
	}

	// Resolve secret ARN by describe (plan uses secret_name)
	var secretARN string
	if strings.TrimSpace(p.Account.SecretName) != "" {
		meta, err := iaws.DescribeSecret(ctx, iaws.DescribeSecretParams{Region: region, Id: p.Account.SecretName})
		if err == nil {
			secretARN = meta.ARN
		}
	}

	// 2) Disassociate secret from cluster if cluster provided
	if strings.TrimSpace(p.AWS.ClusterARN) != "" && secretARN != "" {
		_ = iaws.DisassociateSecrets(ctx, iaws.DisassociateSecretsParams{Region: region, ClusterARN: p.AWS.ClusterARN, SecretARNs: []string{secretARN}})
	}

	// 3) Delete secret (schedule or force)
	if strings.TrimSpace(p.Account.SecretName) != "" {
		_ = iaws.DeleteSecret(ctx, iaws.DeleteSecretParams{Region: region, Id: p.Account.SecretName, Force: opt.ForceSecretDelete})
	}

	// 4) If plan created a new KMS key, schedule its deletion
	if p.Account.KMS.Create {
		if secretARN == "" {
			// try to discover KMS key id used by secret name
			meta, err := iaws.DescribeSecret(ctx, iaws.DescribeSecretParams{Region: region, Id: p.Account.SecretName})
			if err == nil && strings.TrimSpace(meta.KMSKeyID) != "" {
				_ = iaws.ScheduleKMSKeyDeletion(ctx, region, meta.KMSKeyID, opt.KMSPendingWindowDays)
			}
		} else {
			// we don't have kms id directly; attempt describe again by arn
			meta, err := iaws.DescribeSecret(ctx, iaws.DescribeSecretParams{Region: region, Id: secretARN})
			if err == nil && strings.TrimSpace(meta.KMSKeyID) != "" {
				_ = iaws.ScheduleKMSKeyDeletion(ctx, region, meta.KMSKeyID, opt.KMSPendingWindowDays)
			}
		}
	}

	return nil
}

func fmtBool(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
