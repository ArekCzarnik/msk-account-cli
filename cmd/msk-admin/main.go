package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	iaws "github.com/czarnik/msk-account-cli/internal/aws"
	"github.com/czarnik/msk-account-cli/internal/config"
	"github.com/czarnik/msk-account-cli/internal/kafka"
	"github.com/czarnik/msk-account-cli/internal/output"
	"github.com/spf13/cobra"
)

var (
	outputFormat string
)

// ---- Test hooks (overridable in tests) ----
// AWS wrappers
var (
	awsCreateSecret           = iaws.CreateSecret
	awsGetSecret              = iaws.GetSecret
	awsDeleteSecret           = iaws.DeleteSecret
	awsDescribeSecret         = iaws.DescribeSecret
	awsCreateKMSKey           = iaws.CreateKMSKey
	awsScheduleKMSKeyDeletion = iaws.ScheduleKMSKeyDeletion
	awsListMSKSecrets         = iaws.ListMSKSecrets
	awsAssociateSecrets       = iaws.AssociateSecrets
	awsDisassociateSecrets    = iaws.DisassociateSecrets
	awsListClusters           = iaws.ListClusters
	awsListBrokers            = iaws.ListBrokers
)

// Kafka admin abstraction to allow injection in tests
type kafkaAdmin interface {
	CreateACL(ctx context.Context, p kafka.CreateACLParams) error
	ListACLs(ctx context.Context, p kafka.ListACLsParams) ([]kafka.ACLEntry, error)
	DeleteACLs(ctx context.Context, p kafka.DeleteACLsParams) ([]kafka.ACLEntry, error)
	ListConsumerGroups(ctx context.Context) ([]string, error)
	DescribeConsumerGroup(ctx context.Context, groupID string) (*kafka.ConsumerGroupDescription, error)
	DeleteConsumerGroups(ctx context.Context, groupIDs []string) (map[string]error, error)
	Close() error
}

var newKafkaAdmin = func(ctx context.Context, a kafka.AuthConfig) (kafkaAdmin, error) {
	return kafka.NewAdmin(ctx, a)
}

func main() {
	root := &cobra.Command{
		Use:   "msk-admin",
		Short: "Administer MSK SCRAM accounts, Kafka ACLs and consumer groups",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Load config once
			cfg, _, err := config.LoadAppConfig("")
			if err != nil {
				return err
			}

			// Default output from config if flag not provided
			if !cmd.Flags().Changed("output") {
				if v := strings.ToLower(strings.TrimSpace(cfg.Output)); v == "table" || v == "json" {
					outputFormat = v
				}
			}
			of := strings.ToLower(outputFormat)
			if of == "" {
				outputFormat = "table"
				return nil
			}
			if of != "table" && of != "json" {
				return fmt.Errorf("invalid --output %q (must be 'table' or 'json')", outputFormat)
			}
			return nil
		},
	}
	root.PersistentFlags().StringVarP(&outputFormat, "output", "o", "table", "Output format: table|json")

	// account commands
	root.AddCommand(cmdAccount())
	// msk commands
	root.AddCommand(cmdMSK())
	// acl commands
	root.AddCommand(cmdACL())
	// group commands
	root.AddCommand(cmdGroup())
	// gui command
	root.AddCommand(cmdGUI())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// ------------------------
// account commands
// ------------------------

func cmdAccount() *cobra.Command {
	account := &cobra.Command{Use: "account", Short: "Manage MSK SCRAM accounts (Secrets Manager)"}
	account.AddCommand(cmdAccountCreate())
	account.AddCommand(cmdAccountGet())
	account.AddCommand(cmdAccountDelete())
	account.AddCommand(cmdAccountList())
	return account
}

func cmdAccountCreate() *cobra.Command {
	var region, secretName, kmsKeyID, username, password string
	var tagsKV []string
	var createKMS bool
	var kmsDesc string
	var kmsTagsKV []string
	var kmsMultiRegion bool

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create SCRAM credentials secret in AWS Secrets Manager",
		RunE: func(cmd *cobra.Command, args []string) error {
			if password == "" {

				if p := os.Getenv("MSK_ADMIN_PASSWORD"); p != "" {
					password = p
				} else {

					b, err := ioReadAllStdinIfPiped()
					if err == nil && len(b) > 0 {
						password = strings.TrimSpace(string(b))
					}
				}
			}

			v := config.Validator{}
			if err := v.ValidateSecretName(secretName); err != nil {
				return err
			}
			if err := v.ValidateSecretPayload(username, password); err != nil {
				return err
			}
			tagsMap := parseKeyValuePairs(tagsKV)

			// Resolve region (flag or config)
			regionEff := strings.TrimSpace(region)
			if regionEff == "" {
				regionEff = strings.TrimSpace(config.GetDefaultRegion())
			}
			if regionEff == "" {
				return errors.New("region is required (flag or config)")
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()

			// Resolve KMS key: use flag > config default > create if requested
			if kmsKeyID == "" {
				if !createKMS {
					kmsKeyID = strings.TrimSpace(config.GetDefaultKMSKeyID())
					if kmsKeyID == "" {
						return errors.New("--kms-key-id is required unless --create-kms-key is specified (or set in config)")
					}
				} else {
					kmsRes, err := iaws.CreateKMSKey(ctx, iaws.CreateKMSKeyParams{
						Region:      regionEff,
						Description: kmsDesc,
						MultiRegion: kmsMultiRegion,
						Tags:        parseKeyValuePairs(kmsTagsKV),
					})
					if err != nil {
						return err
					}
					kmsKeyID = kmsRes.ARN // Use ARN
				}
			} else if createKMS {
				return errors.New("cannot use --kms-key-id together with --create-kms-key")
			}

			if err := v.ValidateKMSKeyID(kmsKeyID); err != nil {
				return err
			}

			arn, err := awsCreateSecret(ctx, iaws.CreateSecretParams{
				Region:     regionEff,
				SecretName: secretName,
				KMSKeyID:   kmsKeyID,
				Username:   username,
				Password:   password,
				Tags:       tagsMap,
			})
			if err != nil {
				return err
			}
			fmt.Fprintln(os.Stdout, arn)
			return nil
		},
	}
	cmd.Flags().StringVar(&region, "region", "", "AWS region")
	cmd.Flags().StringVar(&secretName, "secret-name", "", "Secret name (must start with AmazonMSK_)")
	cmd.Flags().StringVar(&kmsKeyID, "kms-key-id", "", "KMS key ID or ARN (customer-managed; alias not allowed)")
	cmd.Flags().StringVar(&username, "username", "", "SCRAM username")
	cmd.Flags().StringVar(&password, "password", "", "SCRAM password (omit to read from env MSK_ADMIN_PASSWORD or stdin)")
	cmd.Flags().StringArrayVar(&tagsKV, "tags", nil, "Tags as key=value (repeatable)")
	// Optional: create a KMS key on the fly
	cmd.Flags().BoolVar(&createKMS, "create-kms-key", false, "Create a customer-managed KMS key for the secret (use when --kms-key-id is not provided)")
	cmd.Flags().StringVar(&kmsDesc, "kms-key-description", "", "Description for the created KMS key")
	cmd.Flags().StringArrayVar(&kmsTagsKV, "kms-key-tag", nil, "Tag for the created KMS key as key=value (repeatable)")
	cmd.Flags().BoolVar(&kmsMultiRegion, "kms-key-multi-region", false, "Create the KMS key as multi-Region")
	_ = cmd.MarkFlagRequired("secret-name")
	// kms-key-id is conditionally required; do not mark as required here
	_ = cmd.MarkFlagRequired("username")
	return cmd
}

func cmdAccountGet() *cobra.Command {
	var region, secretARN, secretName string
	var showPassword bool
	cmd := &cobra.Command{
		Use:   "get",
		Short: "Get SCRAM credentials from AWS Secrets Manager",
		RunE: func(cmd *cobra.Command, args []string) error {
			if secretARN == "" && secretName == "" {
				return errors.New("either --secret-arn or --secret-name must be provided")
			}
			regionEff := strings.TrimSpace(region)
			if regionEff == "" {
				regionEff = strings.TrimSpace(config.GetDefaultRegion())
			}
			if regionEff == "" {
				return errors.New("region is required (flag or config)")
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			res, err := awsGetSecret(ctx, iaws.GetSecretParams{Region: regionEff, SecretARN: secretARN, SecretName: secretName})
			if err != nil {
				return err
			}
			if strings.ToLower(outputFormat) == "json" {
				return output.PrintJSON(map[string]string{
					"username": res.Username,
					"password": func() string {
						if showPassword {
							return res.Password
						}
						return ""
					}(),
				})
			}
			if showPassword {
				fmt.Fprintf(os.Stdout, "username: %s\npassword: %s\n", res.Username, res.Password)
			} else {
				fmt.Fprintf(os.Stdout, "username: %s\n", res.Username)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&region, "region", "", "AWS region")
	cmd.Flags().StringVar(&secretARN, "secret-arn", "", "Secret ARN")
	cmd.Flags().StringVar(&secretName, "secret-name", "", "Secret name")
	cmd.Flags().BoolVar(&showPassword, "show-password", false, "Print password (defaults to false)")
	// region may come from config
	return cmd
}

func cmdAccountDelete() *cobra.Command {
	var region, secretName string
	var force bool
	var recoveryDays int32
	var deleteKMS bool
	var kmsPendingDays int32
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete the SCRAM secret (and optionally schedule deletion of its KMS key)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if secretName == "" {
				return errors.New("--secret-name is required")
			}
			regionEff := strings.TrimSpace(region)
			if regionEff == "" {
				regionEff = strings.TrimSpace(config.GetDefaultRegion())
			}
			if regionEff == "" {
				return errors.New("region is required (flag or config)")
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()

			// Find KMS key id via DescribeSecret
			var kmsKeyID string
			if deleteKMS {
				meta, err := awsDescribeSecret(ctx, iaws.DescribeSecretParams{Region: regionEff, Id: secretName})
				if err != nil {
					return fmt.Errorf("describe secret failed: %w", err)
				}
				kmsKeyID = meta.KMSKeyID
				if kmsKeyID == "" {
					return errors.New("secret has no KMS key id; cannot delete KMS key")
				}
			}

			// Delete the secret
			if err := awsDeleteSecret(ctx, iaws.DeleteSecretParams{
				Region:             regionEff,
				Id:                 secretName,
				Force:              force,
				RecoveryWindowDays: recoveryDays,
			}); err != nil {
				return err
			}

			// Optionally schedule KMS key deletion
			if deleteKMS {
				if kmsPendingDays == 0 {
					kmsPendingDays = 30
				}
				if kmsPendingDays < 7 || kmsPendingDays > 30 {
					return errors.New("--kms-pending-window-days must be between 7 and 30")
				}
				if err := awsScheduleKMSKeyDeletion(ctx, regionEff, kmsKeyID, kmsPendingDays); err != nil {
					return fmt.Errorf("schedule KMS key deletion failed: %w", err)
				}
			}

			fmt.Fprintln(os.Stdout, "deleted")
			return nil
		},
	}
	cmd.Flags().StringVar(&region, "region", "", "AWS region")
	cmd.Flags().StringVar(&secretName, "secret-name", "", "Secret name")
	cmd.Flags().BoolVar(&force, "force", false, "Force delete secret without recovery (irreversible)")
	cmd.Flags().Int32Var(&recoveryDays, "recovery-window-days", 30, "Recovery window in days (7..30) when not using --force")
	cmd.Flags().BoolVar(&deleteKMS, "delete-kms-key", false, "Also schedule deletion of the KMS key used by the secret")
	cmd.Flags().Int32Var(&kmsPendingDays, "kms-pending-window-days", 30, "KMS key deletion pending window days (7..30)")
	_ = cmd.MarkFlagRequired("secret-name")
	return cmd
}

func cmdAccountList() *cobra.Command {
	var region string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List AmazonMSK_ secrets and their ARNs",
		RunE: func(cmd *cobra.Command, args []string) error {
			regionEff := strings.TrimSpace(region)
			if regionEff == "" {
				regionEff = strings.TrimSpace(config.GetDefaultRegion())
			}
			if regionEff == "" {
				return errors.New("region is required (flag or config)")
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			rows, err := awsListMSKSecrets(ctx, regionEff)
			if err != nil {
				return err
			}
			outRows := make([]output.SecretRow, 0, len(rows))
			for _, r := range rows {
				outRows = append(outRows, output.SecretRow{Name: r.Name, ARN: r.ARN})
			}
			return output.FormatAndPrintSecrets(outRows, outputFormat)
		},
	}
	cmd.Flags().StringVar(&region, "region", "", "AWS region")
	return cmd
}

// ------------------------
// MSK commands
// ------------------------

func cmdMSK() *cobra.Command {
	m := &cobra.Command{Use: "msk", Short: "Manage MSK cluster SCRAM associations"}
	m.AddCommand(cmdMSKAssociate())
	m.AddCommand(cmdMSKDisassociate())
	m.AddCommand(cmdMSKListClusters())
	m.AddCommand(cmdMSKListBrokers())
	return m
}

func cmdMSKAssociate() *cobra.Command {
	var region, clusterARN string
	var secretARNs []string
	cmd := &cobra.Command{
		Use:   "associate-secret",
		Short: "Associate SCRAM secrets with an MSK cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(secretARNs) == 0 {
				return errors.New("at least one --secret-arn must be provided")
			}
			// Resolve region and cluster ARN from config if missing
			regionEff := strings.TrimSpace(region)
			if regionEff == "" {
				regionEff = strings.TrimSpace(config.GetDefaultRegion())
			}
			if regionEff == "" {
				return errors.New("region is required (flag or config)")
			}
			clusterARNEff := strings.TrimSpace(clusterARN)
			if clusterARNEff == "" {
				clusterARNEff = strings.TrimSpace(config.GetDefaultClusterARN())
			}
			if clusterARNEff == "" {
				return errors.New("cluster-arn is required (flag or config)")
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			if err := awsAssociateSecrets(ctx, iaws.AssociateSecretsParams{Region: regionEff, ClusterARN: clusterARNEff, SecretARNs: secretARNs}); err != nil {
				return err
			}
			fmt.Fprintln(os.Stdout, "associated")
			return nil
		},
	}
	cmd.Flags().StringVar(&region, "region", "", "AWS region")
	cmd.Flags().StringVar(&clusterARN, "cluster-arn", "", "MSK cluster ARN")
	cmd.Flags().StringArrayVar(&secretARNs, "secret-arn", nil, "Secret ARN (repeatable)")
	// region/cluster-arn may come from config
	return cmd
}

func cmdMSKDisassociate() *cobra.Command {
	var region, clusterARN string
	var secretARNs []string
	cmd := &cobra.Command{
		Use:   "disassociate-secret",
		Short: "Disassociate SCRAM secrets from an MSK cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(secretARNs) == 0 {
				return errors.New("at least one --secret-arn must be provided")
			}
			regionEff := strings.TrimSpace(region)
			if regionEff == "" {
				regionEff = strings.TrimSpace(config.GetDefaultRegion())
			}
			if regionEff == "" {
				return errors.New("region is required (flag or config)")
			}
			clusterARNEff := strings.TrimSpace(clusterARN)
			if clusterARNEff == "" {
				clusterARNEff = strings.TrimSpace(config.GetDefaultClusterARN())
			}
			if clusterARNEff == "" {
				return errors.New("cluster-arn is required (flag or config)")
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			if err := awsDisassociateSecrets(ctx, iaws.DisassociateSecretsParams{Region: regionEff, ClusterARN: clusterARNEff, SecretARNs: secretARNs}); err != nil {
				return err
			}
			fmt.Fprintln(os.Stdout, "disassociated")
			return nil
		},
	}
	cmd.Flags().StringVar(&region, "region", "", "AWS region")
	cmd.Flags().StringVar(&clusterARN, "cluster-arn", "", "MSK cluster ARN")
	cmd.Flags().StringArrayVar(&secretARNs, "secret-arn", nil, "Secret ARN (repeatable)")
	// region/cluster-arn may come from config
	return cmd
}

func cmdMSKListClusters() *cobra.Command {
	var region, namePrefix, columns string
	cmd := &cobra.Command{
		Use:   "list-clusters",
		Short: "List MSK clusters with their ARNs",
		RunE: func(cmd *cobra.Command, args []string) error {
			regionEff := strings.TrimSpace(region)
			if regionEff == "" {
				regionEff = strings.TrimSpace(config.GetDefaultRegion())
			}
			if regionEff == "" {
				return errors.New("region is required (flag or config)")
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			clusters, err := awsListClusters(ctx, regionEff, namePrefix)
			if err != nil {
				return err
			}
			rows := make([]output.ClusterRow, 0, len(clusters))
			for _, c := range clusters {
				rows = append(rows, output.ClusterRow{Name: c.Name, ARN: c.ARN})
			}
			// Enrich state/type in rows; JSON will include them; table optional via --columns
			for i := range rows {
				rows[i].State = clusters[i].State
				rows[i].Type = clusters[i].Type
			}
			var cols []string
			if strings.TrimSpace(columns) != "" {
				cols = splitCSV(columns)
			}
			return output.FormatAndPrintClusters(rows, outputFormat, cols)
		},
	}
	cmd.Flags().StringVar(&region, "region", "", "AWS region")
	cmd.Flags().StringVar(&namePrefix, "name-prefix", "", "Optional cluster name prefix filter")
	cmd.Flags().StringVar(&columns, "columns", "", "Table columns (comma-separated) e.g. name,arn,state,type; default name,arn")
	return cmd
}

func cmdMSKListBrokers() *cobra.Command {
	var region, clusterARN string
	cmd := &cobra.Command{
		Use:   "list-brokers",
		Short: "List Kafka broker nodes for a cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			regionEff := strings.TrimSpace(region)
			if regionEff == "" {
				regionEff = strings.TrimSpace(config.GetDefaultRegion())
			}
			if regionEff == "" {
				return errors.New("region is required (flag or config)")
			}
			clusterARNEff := strings.TrimSpace(clusterARN)
			if clusterARNEff == "" {
				clusterARNEff = strings.TrimSpace(config.GetDefaultClusterARN())
			}
			if clusterARNEff == "" {
				return errors.New("cluster-arn is required (flag or config)")
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			brokers, err := awsListBrokers(ctx, regionEff, clusterARNEff)
			if err != nil {
				return err
			}
			rows := make([]output.BrokerRow, 0, len(brokers))
			for _, b := range brokers {
				rows = append(rows, output.BrokerRow{ID: b.ID, Endpoints: b.Endpoints})
			}
			return output.FormatAndPrintBrokers(rows, outputFormat)
		},
	}
	cmd.Flags().StringVar(&region, "region", "", "AWS region")
	cmd.Flags().StringVar(&clusterARN, "cluster-arn", "", "MSK cluster ARN")
	// region/cluster-arn may come from config
	return cmd
}

// ------------------------
// ACL commands
// ------------------------

func cmdACL() *cobra.Command {
	acl := &cobra.Command{Use: "acl", Short: "Manage Kafka ACLs"}
	acl.AddCommand(cmdACLCreate())
	acl.AddCommand(cmdACLList())
	acl.AddCommand(cmdACLDelete())
	return acl
}

type kafkaAuthFlags struct {
	brokers      string
	saslUsername string
	saslPassword string
	secretARN    string
	region       string
	scramMech    string
}

func (f *kafkaAuthFlags) addFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.brokers, "brokers", "", "Comma-separated bootstrap brokers")
	cmd.Flags().StringVar(&f.saslUsername, "sasl-username", "", "SASL username (optional if --secret-arn used)")
	cmd.Flags().StringVar(&f.saslPassword, "sasl-password", "", "SASL password (optional if --secret-arn used)")
	cmd.Flags().StringVar(&f.secretARN, "secret-arn", "", "Secrets Manager ARN holding SCRAM credentials (optional)")
	cmd.Flags().StringVar(&f.region, "region", "", "AWS region (required when --secret-arn is used)")
	cmd.Flags().StringVar(&f.scramMech, "scram-mechanism", "sha512", "SCRAM mechanism: sha256|sha512")
}

func (f *kafkaAuthFlags) buildAuth(ctx context.Context) (kafka.AuthConfig, error) {
	var ac kafka.AuthConfig
	if strings.TrimSpace(f.brokers) != "" {
		ac.Brokers = strings.Split(f.brokers, ",")
	} else {
		ac.Brokers = config.GetDefaultBrokers()
	}
	if len(ac.Brokers) == 0 {
		return ac, errors.New("--brokers is required (flag or config)")
	}
	ac.SCRAMMechanism = f.scramMech
	if f.secretARN != "" {
		if f.region == "" {
			return ac, errors.New("--region is required when --secret-arn is set")
		}
		sec, err := iaws.GetSecret(ctx, iaws.GetSecretParams{Region: f.region, SecretARN: f.secretARN})
		if err != nil {
			return ac, err
		}
		ac.SASLUsername = sec.Username
		ac.SASLPassword = sec.Password
	} else {
		// Use flag values if set; otherwise fall back to config defaults
		u := strings.TrimSpace(f.saslUsername)
		p := strings.TrimSpace(f.saslPassword)
		if u == "" {
			u = config.GetDefaultSASLUsername()
		}
		if p == "" {
			p = config.GetDefaultSASLPassword()
		}
		ac.SASLUsername = u
		ac.SASLPassword = p
	}
	return ac, ac.Validate()
}

func cmdACLCreate() *cobra.Command {
	var auth kafkaAuthFlags
	var resourceType, resourceName, resourcePattern, principal, host, operation, permission string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a Kafka ACL",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			ac, err := auth.buildAuth(ctx)
			if err != nil {
				return err
			}
			admin, err := newKafkaAdmin(ctx, ac)
			if err != nil {
				return err
			}
			defer admin.Close()

			v := config.Validator{}
			if err := v.ValidateResourceType(resourceType); err != nil {
				return err
			}
			if err := v.ValidateResourcePattern(resourcePattern); err != nil {
				return err
			}
			if err := v.ValidateOperation(operation); err != nil {
				return err
			}
			if err := v.ValidatePermission(permission); err != nil {
				return err
			}

			if err := admin.CreateACL(ctx, kafka.CreateACLParams{
				ResourceType:    resourceType,
				ResourceName:    resourceName,
				ResourcePattern: resourcePattern,
				Principal:       principal,
				Host:            host,
				Operation:       operation,
				Permission:      permission,
			}); err != nil {
				return err
			}
			fmt.Fprintln(os.Stdout, "created")
			return nil
		},
	}
	auth.addFlags(cmd)
	cmd.Flags().StringVar(&resourceType, "resource-type", "", "Resource type: topic|group|cluster|transactionalId")
	cmd.Flags().StringVar(&resourceName, "resource-name", "", "Resource name")
	cmd.Flags().StringVar(&resourcePattern, "resource-pattern", "literal", "Pattern: literal|prefixed")
	cmd.Flags().StringVar(&principal, "principal", "", "Principal, e.g. User:alice")
	cmd.Flags().StringVar(&host, "host", "*", "Host filter (default '*')")
	cmd.Flags().StringVar(&operation, "operation", "", "Operation: read|write|create|delete|alter|describe|...")
	cmd.Flags().StringVar(&permission, "permission", "allow", "Permission: allow|deny")
	_ = cmd.MarkFlagRequired("resource-type")
	_ = cmd.MarkFlagRequired("resource-name")
	_ = cmd.MarkFlagRequired("principal")
	_ = cmd.MarkFlagRequired("operation")
	return cmd
}

func cmdACLList() *cobra.Command {
	var auth kafkaAuthFlags
	var resourceType, resourceName, principal, operation string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List Kafka ACLs",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			ac, err := auth.buildAuth(ctx)
			if err != nil {
				return err
			}
			admin, err := newKafkaAdmin(ctx, ac)
			if err != nil {
				return err
			}
			defer admin.Close()

			acls, err := admin.ListACLs(ctx, kafka.ListACLsParams{
				ResourceType: resourceType,
				ResourceName: resourceName,
				Principal:    principal,
				Operation:    operation,
			})
			if err != nil {
				return err
			}
			return output.FormatAndPrintACLs(acls, outputFormat)
		},
	}
	auth.addFlags(cmd)
	cmd.Flags().StringVar(&resourceType, "resource-type", "", "Filter by resource type")
	cmd.Flags().StringVar(&resourceName, "resource-name", "", "Filter by resource name")
	cmd.Flags().StringVar(&principal, "principal", "", "Filter by principal")
	cmd.Flags().StringVar(&operation, "operation", "", "Filter by operation")
	return cmd
}

func cmdACLDelete() *cobra.Command {
	var auth kafkaAuthFlags
	var resourceType, resourceName, resourcePattern, principal, host, operation, permission string
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete matching Kafka ACLs",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			ac, err := auth.buildAuth(ctx)
			if err != nil {
				return err
			}
			admin, err := newKafkaAdmin(ctx, ac)
			if err != nil {
				return err
			}
			defer admin.Close()

			v := config.Validator{}
			if resourceType != "" {
				if err := v.ValidateResourceType(resourceType); err != nil {
					return err
				}
			}
			if resourcePattern != "" {
				if err := v.ValidateResourcePattern(resourcePattern); err != nil {
					return err
				}
			}
			if operation != "" {
				if err := v.ValidateOperation(operation); err != nil {
					return err
				}
			}
			if permission != "" {
				if err := v.ValidatePermission(permission); err != nil {
					return err
				}
			}

			deleted, err := admin.DeleteACLs(ctx, kafka.DeleteACLsParams{
				ResourceType:    resourceType,
				ResourceName:    resourceName,
				ResourcePattern: resourcePattern,
				Principal:       principal,
				Host:            host,
				Operation:       operation,
				Permission:      permission,
			})
			if err != nil {
				return err
			}
			return output.FormatAndPrintACLs(deleted, outputFormat)
		},
	}
	auth.addFlags(cmd)
	cmd.Flags().StringVar(&resourceType, "resource-type", "", "Resource type: topic|group|cluster|transactionalId")
	cmd.Flags().StringVar(&resourceName, "resource-name", "", "Resource name")
	cmd.Flags().StringVar(&resourcePattern, "resource-pattern", "", "Pattern: literal|prefixed")
	cmd.Flags().StringVar(&principal, "principal", "", "Principal, e.g. User:alice")
	cmd.Flags().StringVar(&host, "host", "", "Host filter")
	cmd.Flags().StringVar(&operation, "operation", "", "Operation")
	cmd.Flags().StringVar(&permission, "permission", "", "Permission: allow|deny")
	return cmd
}

// ------------------------
// Group commands
// ------------------------

func cmdGroup() *cobra.Command {
	g := &cobra.Command{Use: "group", Short: "Manage Kafka consumer groups"}
	g.AddCommand(cmdGroupList())
	g.AddCommand(cmdGroupDescribe())
	g.AddCommand(cmdGroupDelete())
	return g
}

func cmdGroupList() *cobra.Command {
	var auth kafkaAuthFlags
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List consumer groups",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			ac, err := auth.buildAuth(ctx)
			if err != nil {
				return err
			}
			admin, err := newKafkaAdmin(ctx, ac)
			if err != nil {
				return err
			}
			defer admin.Close()
			groups, err := admin.ListConsumerGroups(ctx)
			if err != nil {
				return err
			}
			return output.FormatAndPrintConsumerGroups(groups, outputFormat)
		},
	}
	auth.addFlags(cmd)
	return cmd
}

func cmdGroupDescribe() *cobra.Command {
	var auth kafkaAuthFlags
	var groupID string
	cmd := &cobra.Command{
		Use:   "describe",
		Short: "Describe a consumer group",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			ac, err := auth.buildAuth(ctx)
			if err != nil {
				return err
			}
			admin, err := newKafkaAdmin(ctx, ac)
			if err != nil {
				return err
			}
			defer admin.Close()
			desc, err := admin.DescribeConsumerGroup(ctx, groupID)
			if err != nil {
				return err
			}
			return output.FormatAndPrintConsumerGroupDescription(desc, outputFormat)
		},
	}
	auth.addFlags(cmd)
	cmd.Flags().StringVar(&groupID, "group-id", "", "Consumer group ID")
	_ = cmd.MarkFlagRequired("group-id")
	return cmd
}

func cmdGroupDelete() *cobra.Command {
	var auth kafkaAuthFlags
	var groupIDs []string
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete consumer groups",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			ac, err := auth.buildAuth(ctx)
			if err != nil {
				return err
			}
			admin, err := newKafkaAdmin(ctx, ac)
			if err != nil {
				return err
			}
			defer admin.Close()
			results, err := admin.DeleteConsumerGroups(ctx, groupIDs)
			if err != nil {
				return err
			}

			return output.FormatAndPrintGroupDeleteResults(results, outputFormat)
		},
	}
	auth.addFlags(cmd)
	cmd.Flags().StringArrayVar(&groupIDs, "group-id", nil, "Consumer group ID (repeatable)")
	_ = cmd.MarkFlagRequired("group-id")
	return cmd
}

// Helpers
func parseKeyValuePairs(kv []string) map[string]string {
	m := map[string]string{}
	for _, s := range kv {
		if s == "" {
			continue
		}
		parts := strings.SplitN(s, "=", 2)
		if len(parts) != 2 {
			continue
		}
		k := strings.TrimSpace(parts[0])
		v := strings.TrimSpace(parts[1])
		if k != "" {
			m[k] = v
		}
	}
	return m
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func ioReadAllStdinIfPiped() ([]byte, error) {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return nil, err
	}
	if (fi.Mode() & os.ModeCharDevice) == 0 {
		// data is being piped
		return ioReadAll(os.Stdin)
	}
	return nil, nil
}

func ioReadAll(f *os.File) ([]byte, error) {
	const max = 1024 * 1024 // 1MB
	b := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, err := f.Read(tmp)
		if n > 0 {
			b = append(b, tmp[:n]...)
		}
		if err != nil {
			if errors.Is(err, os.ErrClosed) || err.Error() == "EOF" {
				return b, nil
			}
			return b, nil
		}
		if len(b) > max {
			return b, nil
		}
	}
}
