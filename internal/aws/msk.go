package aws

import (
	"context"
	"fmt"
	"strconv"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	ktypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
)

type AssociateSecretsParams struct {
	Region     string
	ClusterARN string
	SecretARNs []string
}

func AssociateSecrets(ctx context.Context, p AssociateSecretsParams) error {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(p.Region))
	if err != nil {
		return err
	}
	cli := kafka.NewFromConfig(cfg)
	out, err := cli.BatchAssociateScramSecret(ctx, &kafka.BatchAssociateScramSecretInput{
		ClusterArn:    &p.ClusterARN,
		SecretArnList: p.SecretARNs,
	})
	if err != nil {
		return err
	}
	if out.UnprocessedScramSecrets != nil && len(out.UnprocessedScramSecrets) > 0 {
		return fmt.Errorf("%d secrets unprocessed by BatchAssociateScramSecret", len(out.UnprocessedScramSecrets))
	}
	return nil
}

type DisassociateSecretsParams struct {
	Region     string
	ClusterARN string
	SecretARNs []string
}

func DisassociateSecrets(ctx context.Context, p DisassociateSecretsParams) error {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(p.Region))
	if err != nil {
		return err
	}
	cli := kafka.NewFromConfig(cfg)
	out, err := cli.BatchDisassociateScramSecret(ctx, &kafka.BatchDisassociateScramSecretInput{
		ClusterArn:    &p.ClusterARN,
		SecretArnList: p.SecretARNs,
	})
	if err != nil {
		return err
	}
	if out.UnprocessedScramSecrets != nil && len(out.UnprocessedScramSecrets) > 0 {
		return fmt.Errorf("%d secrets unprocessed by BatchDisassociateScramSecret", len(out.UnprocessedScramSecrets))
	}
	return nil
}

// MSKClusterSummary represents a minimal cluster view.
type MSKClusterSummary struct {
	Name  string
	ARN   string
	State string
	Type  string
}

// ListClusters returns MSK clusters (name + ARN). Optional namePrefix filters by cluster name prefix.
func ListClusters(ctx context.Context, region string, namePrefix string) ([]MSKClusterSummary, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, err
	}
	cli := kafka.NewFromConfig(cfg)
	in := &kafka.ListClustersV2Input{}
	if namePrefix != "" {
		in.ClusterNameFilter = &namePrefix
	}
	p := kafka.NewListClustersV2Paginator(cli, in)
	var out []MSKClusterSummary
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, c := range page.ClusterInfoList {
			row := MSKClusterSummary{
				Name:  deref(c.ClusterName),
				ARN:   deref(c.ClusterArn),
				State: string(c.State),
				Type:  clusterTypeToString(c.ClusterType),
			}
			out = append(out, row)
		}
	}
	return out, nil
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
func clusterTypeToString(t ktypes.ClusterType) string {
	if t == "" {
		return ""
	}
	return string(t)
}

// Broker endpoint row
type BrokerEndpoint struct {
	ID        string
	Endpoints []string
}

// ListBrokers enumerates broker nodes and returns their endpoints.
func ListBrokers(ctx context.Context, region, clusterARN string) ([]BrokerEndpoint, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, err
	}
	cli := kafka.NewFromConfig(cfg)
	in := &kafka.ListNodesInput{ClusterArn: &clusterARN}
	p := kafka.NewListNodesPaginator(cli, in)
	var out []BrokerEndpoint
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, n := range page.NodeInfoList {
			if n.BrokerNodeInfo != nil {
				id := ""
				if n.BrokerNodeInfo.BrokerId != nil {
					// BrokerId is float64 in SDK types; render as integer when possible
					id = strconv.FormatInt(int64(*n.BrokerNodeInfo.BrokerId), 10)
				} else if n.NodeARN != nil {
					id = *n.NodeARN
				}
				out = append(out, BrokerEndpoint{ID: id, Endpoints: n.BrokerNodeInfo.Endpoints})
			}
		}
	}
	return out, nil
}
