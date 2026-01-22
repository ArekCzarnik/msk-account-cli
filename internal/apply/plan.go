package apply

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Plan models the YAML used to provision an account + ACLs.
type Plan struct {
	Version string `yaml:"version"`
	Kind    string `yaml:"kind"`

	Metadata struct {
		Name        string `yaml:"name"`
		Environment string `yaml:"environment"`
	} `yaml:"metadata"`

	AWS struct {
		Region     string `yaml:"region"`
		ClusterARN string `yaml:"cluster_arn"`
	} `yaml:"aws"`

	AdminConnection struct {
		Brokers []string `yaml:"brokers"`
		Auth    struct {
			SecretARN      string `yaml:"secret_arn"`
			Region         string `yaml:"region"`
			SASLUsername   string `yaml:"sasl_username"`
			SASLPassword   string `yaml:"sasl_password"`
			SCRAMMechanism string `yaml:"scram_mechanism"`
		} `yaml:"auth"`
	} `yaml:"admin_connection"`

	Account struct {
		SecretName      string            `yaml:"secret_name"`
		Username        string            `yaml:"username"`
		Password        string            `yaml:"password"`
		PasswordFromEnv string            `yaml:"password_from_env"`
		Tags            map[string]string `yaml:"tags"`
		KMS             struct {
			UseExistingKey bool              `yaml:"use_existing_key"`
			KMSKeyID       string            `yaml:"kms_key_id"`
			Create         bool              `yaml:"create"`
			Description    string            `yaml:"description"`
			MultiRegion    bool              `yaml:"multi_region"`
			Tags           map[string]string `yaml:"tags"`
		} `yaml:"kms"`
		AssociateWithCluster bool `yaml:"associate_with_cluster"`
	} `yaml:"account"`

	ACLs []ACLItem `yaml:"acls"`

	Topics []TopicItem `yaml:"topics"`
}

type ACLItem struct {
	ResourceType    string `yaml:"resource_type"`
	ResourceName    string `yaml:"resource_name"`
	ResourcePattern string `yaml:"resource_pattern"`
	Principal       string `yaml:"principal"`
	Host            string `yaml:"host"`
	Operation       string `yaml:"operation"`
	Permission      string `yaml:"permission"`
}

type TopicItem struct {
	Name              string            `yaml:"name"`
	Partitions        int32             `yaml:"partitions"`
	ReplicationFactor int16             `yaml:"replication_factor"`
	Configs           map[string]string `yaml:"configs"`
	CreateIfMissing   bool              `yaml:"create_if_missing"`
}

func LoadPlan(path string) (*Plan, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p Plan
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.Version) == "" {
		return nil, fmt.Errorf("missing version in plan")
	}
	return &p, nil
}

// ExpandPlaceholders replaces ${account.username} etc. in strings.
func (p *Plan) ExpandPlaceholders(s string) string {
	if s == "" {
		return s
	}
	out := strings.ReplaceAll(s, "${account.username}", p.Account.Username)
	return out
}
