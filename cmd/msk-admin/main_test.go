package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	iaws "github.com/czarnik/msk-account-cli/internal/aws"
	"github.com/czarnik/msk-account-cli/internal/kafka"
)

// captureOutput redirects stdout/stderr while fn runs
func captureOutput(t *testing.T, fn func()) (string, string) {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout, os.Stderr = wOut, wErr
	defer func() {
		_ = wOut.Close()
		_ = wErr.Close()
		os.Stdout, os.Stderr = oldOut, oldErr
	}()
	done := make(chan struct{})
	var bufOut, bufErr bytes.Buffer
	go func() { _, _ = io.Copy(&bufOut, rOut); close(done) }()
	go func() { _, _ = io.Copy(&bufErr, rErr) }()

	fn()

	_ = wOut.Close()
	_ = wErr.Close()
	<-done
	return bufOut.String(), bufErr.String()
}

// ---- Kafka fake admin ----
type fakeKafkaAdmin struct {
	createdParams   *kafka.CreateACLParams
	listParams      *kafka.ListACLsParams
	deleteParams    *kafka.DeleteACLsParams
	listGroupsOut   []string
	listACLsOut     []kafka.ACLEntry
	deleteACLsOut   []kafka.ACLEntry
	describeOut     *kafka.ConsumerGroupDescription
	deleteGroupsRes map[string]error
	err             error
}

func (f *fakeKafkaAdmin) CreateACL(ctx context.Context, p kafka.CreateACLParams) error {
	cp := p
	f.createdParams = &cp
	return f.err
}
func (f *fakeKafkaAdmin) ListACLs(ctx context.Context, p kafka.ListACLsParams) ([]kafka.ACLEntry, error) {
	lp := p
	f.listParams = &lp
	return f.listACLsOut, f.err
}
func (f *fakeKafkaAdmin) DeleteACLs(ctx context.Context, p kafka.DeleteACLsParams) ([]kafka.ACLEntry, error) {
	dp := p
	f.deleteParams = &dp
	return f.deleteACLsOut, f.err
}
func (f *fakeKafkaAdmin) ListConsumerGroups(ctx context.Context) ([]string, error) {
	return f.listGroupsOut, f.err
}
func (f *fakeKafkaAdmin) DescribeConsumerGroup(ctx context.Context, groupID string) (*kafka.ConsumerGroupDescription, error) {
	return f.describeOut, f.err
}
func (f *fakeKafkaAdmin) DeleteConsumerGroups(ctx context.Context, groupIDs []string) (map[string]error, error) {
	return f.deleteGroupsRes, f.err
}
func (f *fakeKafkaAdmin) Close() error { return nil }

// ---- Tests per command group ----

func TestAccountCreate_Success(t *testing.T) {
	t.Cleanup(func() { // restore hooks
		awsCreateSecret = iaws.CreateSecret
	})
	var seen iaws.CreateSecretParams
	awsCreateSecret = func(ctx context.Context, p iaws.CreateSecretParams) (string, error) {
		seen = p
		return "arn:aws:secretsmanager:eu-central-1:1111:secret:AmazonMSK_alice-XYZ", nil
	}
	outputFormat = "table"
	cmd := cmdAccountCreate()
	cmd.SetArgs([]string{
		"--region", "eu-central-1",
		"--secret-name", "AmazonMSK_alice",
		"--kms-key-id", "arn:aws:kms:eu-central-1:1111:key/abcd",
		"--username", "alice",
		"--password", "pw",
		"--tags", "env=dev",
		"--tags", "owner=team",
	})
	out, _ := captureOutput(t, func() {
		_ = cmd.Execute()
	})
	if !strings.Contains(out, "arn:aws:secretsmanager") {
		t.Fatalf("expected ARN in output, got %q", out)
	}
	if seen.SecretName != "AmazonMSK_alice" || seen.Username != "alice" || seen.Password != "pw" {
		t.Fatalf("unexpected params seen: %+v", seen)
	}
	if seen.Tags["env"] != "dev" || seen.Tags["owner"] != "team" {
		t.Fatalf("tags not propagated: %+v", seen.Tags)
	}
}

func TestAccountGet_JSON_ShowPassword(t *testing.T) {
	t.Cleanup(func() { awsGetSecret = iaws.GetSecret })
	awsGetSecret = func(ctx context.Context, p iaws.GetSecretParams) (*iaws.GetSecretResult, error) {
		return &iaws.GetSecretResult{Username: "bob", Password: "secret"}, nil
	}
	outputFormat = "json"
	cmd := cmdAccountGet()
	cmd.SetArgs([]string{"--region", "eu", "--secret-name", "AmazonMSK_bob", "--show-password"})
	out, _ := captureOutput(t, func() { _ = cmd.Execute() })
	var m map[string]string
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("invalid json: %v; out=%q", err, out)
	}
	if m["username"] != "bob" || m["password"] != "secret" {
		t.Fatalf("unexpected payload: %#v", m)
	}
}

func TestAccountDelete_Force_NoKMS(t *testing.T) {
	t.Cleanup(func() { awsDeleteSecret = iaws.DeleteSecret })
	var seen iaws.DeleteSecretParams
	awsDeleteSecret = func(ctx context.Context, p iaws.DeleteSecretParams) error {
		seen = p
		return nil
	}
	outputFormat = "table"
	cmd := cmdAccountDelete()
	cmd.SetArgs([]string{"--region", "eu", "--secret-name", "AmazonMSK_x", "--force"})
	out, _ := captureOutput(t, func() { _ = cmd.Execute() })
	if !strings.Contains(out, "deleted") {
		t.Fatalf("expected 'deleted' msg, got %q", out)
	}
	if !seen.Force || seen.Id != "AmazonMSK_x" {
		t.Fatalf("unexpected delete params: %+v", seen)
	}
}

func TestAccountDelete_WithKMS(t *testing.T) {
	t.Cleanup(func() {
		awsDescribeSecret = iaws.DescribeSecret
		awsDeleteSecret = iaws.DeleteSecret
		awsScheduleKMSKeyDeletion = iaws.ScheduleKMSKeyDeletion
	})
	awsDescribeSecret = func(ctx context.Context, p iaws.DescribeSecretParams) (*iaws.DescribeSecretResult, error) {
		return &iaws.DescribeSecretResult{ARN: "arn:...", KMSKeyID: "key-123"}, nil
	}
	deletedCalled := false
	awsDeleteSecret = func(ctx context.Context, p iaws.DeleteSecretParams) error {
		deletedCalled = true
		return nil
	}
	var kmspending int32
	var kmskey string
	awsScheduleKMSKeyDeletion = func(ctx context.Context, region, keyID string, pendingWindowDays int32) error {
		kmskey = keyID
		kmspending = pendingWindowDays
		return nil
	}
	cmd := cmdAccountDelete()
	cmd.SetArgs([]string{"--region", "eu", "--secret-name", "AmazonMSK_x", "--delete-kms-key", "--kms-pending-window-days", "7"})
	_, _ = captureOutput(t, func() { _ = cmd.Execute() })
	if !deletedCalled || kmskey != "key-123" || kmspending != 7 {
		t.Fatalf("expected kms delete scheduled; got key=%s days=%d deleted=%v", kmskey, kmspending, deletedCalled)
	}
}

func TestAccountList_JSON(t *testing.T) {
	t.Cleanup(func() { awsListMSKSecrets = iaws.ListMSKSecrets })
	awsListMSKSecrets = func(ctx context.Context, region string) ([]iaws.SecretSummary, error) {
		return []iaws.SecretSummary{{Name: "AmazonMSK_a", ARN: "arn:a"}, {Name: "AmazonMSK_b", ARN: "arn:b"}}, nil
	}
	outputFormat = "json"
	cmd := cmdAccountList()
	cmd.SetArgs([]string{"--region", "eu"})
	out, _ := captureOutput(t, func() { _ = cmd.Execute() })
	if !strings.Contains(out, "AmazonMSK_a") || !strings.Contains(out, "arn:b") {
		t.Fatalf("unexpected list json: %s", out)
	}
}

func TestMSKAssociate(t *testing.T) {
	t.Cleanup(func() { awsAssociateSecrets = iaws.AssociateSecrets })
	var seen iaws.AssociateSecretsParams
	awsAssociateSecrets = func(ctx context.Context, p iaws.AssociateSecretsParams) error {
		seen = p
		return nil
	}
	cmd := cmdMSKAssociate()
	cmd.SetArgs([]string{"--region", "eu", "--cluster-arn", "arn:cluster", "--secret-arn", "arn:sec1", "--secret-arn", "arn:sec2"})
	out, _ := captureOutput(t, func() { _ = cmd.Execute() })
	if !strings.Contains(out, "associated") {
		t.Fatalf("expected associated message, got %q", out)
	}
	if seen.ClusterARN != "arn:cluster" || len(seen.SecretARNs) != 2 {
		t.Fatalf("unexpected associate params: %+v", seen)
	}
}

func TestMSKDisassociate(t *testing.T) {
	t.Cleanup(func() { awsDisassociateSecrets = iaws.DisassociateSecrets })
	var seen iaws.DisassociateSecretsParams
	awsDisassociateSecrets = func(ctx context.Context, p iaws.DisassociateSecretsParams) error {
		seen = p
		return nil
	}
	cmd := cmdMSKDisassociate()
	cmd.SetArgs([]string{"--region", "eu", "--cluster-arn", "arn:cluster", "--secret-arn", "arn:sec1"})
	out, _ := captureOutput(t, func() { _ = cmd.Execute() })
	if !strings.Contains(out, "disassociated") {
		t.Fatalf("expected disassociated message, got %q", out)
	}
	if seen.ClusterARN != "arn:cluster" || len(seen.SecretARNs) != 1 {
		t.Fatalf("unexpected disassociate params: %+v", seen)
	}
}

func TestMSKListClusters_JSON(t *testing.T) {
	t.Cleanup(func() { awsListClusters = iaws.ListClusters })
	awsListClusters = func(ctx context.Context, region string, namePrefix string) ([]iaws.MSKClusterSummary, error) {
		return []iaws.MSKClusterSummary{{Name: "dev-a", ARN: "arn:a", State: "ACTIVE", Type: "PROVISIONED"}}, nil
	}
	outputFormat = "json"
	cmd := cmdMSKListClusters()
	cmd.SetArgs([]string{"--region", "eu"})
	out, _ := captureOutput(t, func() { _ = cmd.Execute() })
	if !strings.Contains(out, "\"name\": \"dev-a\"") || !strings.Contains(out, "\"state\": \"ACTIVE\"") {
		t.Fatalf("unexpected clusters json: %s", out)
	}
}

func TestMSKListBrokers_JSON(t *testing.T) {
	t.Cleanup(func() { awsListBrokers = iaws.ListBrokers })
	awsListBrokers = func(ctx context.Context, region, clusterARN string) ([]iaws.BrokerEndpoint, error) {
		return []iaws.BrokerEndpoint{{ID: "1", Endpoints: []string{"b-1:9096"}}}, nil
	}
	outputFormat = "json"
	cmd := cmdMSKListBrokers()
	cmd.SetArgs([]string{"--region", "eu", "--cluster-arn", "arn:cluster"})
	out, _ := captureOutput(t, func() { _ = cmd.Execute() })
	if !strings.Contains(out, "\"id\": \"1\"") || !strings.Contains(out, "b-1:9096") {
		t.Fatalf("unexpected brokers json: %s", out)
	}
}

func TestACLCreate_InvokesAdmin(t *testing.T) {
	var fake fakeKafkaAdmin
	t.Cleanup(func() {
		newKafkaAdmin = func(ctx context.Context, a kafka.AuthConfig) (kafkaAdmin, error) { return &fakeKafkaAdmin{}, nil }
	})
	newKafkaAdmin = func(ctx context.Context, a kafka.AuthConfig) (kafkaAdmin, error) { return &fake, nil }
	cmd := cmdACLCreate()
	cmd.SetArgs([]string{
		"--brokers", "b1:9096",
		"--sasl-username", "u",
		"--sasl-password", "p",
		"--resource-type", "topic",
		"--resource-name", "foo",
		"--principal", "User:alice",
		"--operation", "read",
	})
	_, _ = captureOutput(t, func() { _ = cmd.Execute() })
	if fake.createdParams == nil || fake.createdParams.ResourceName != "foo" || fake.createdParams.Operation != "read" {
		t.Fatalf("create not called with expected params: %+v", fake.createdParams)
	}
}

func TestACLList_JSON(t *testing.T) {
	var fake fakeKafkaAdmin
	fake.listACLsOut = []kafka.ACLEntry{{ResourceType: "topic", ResourceName: "foo", Principal: "User:alice", Operation: "read", Permission: "allow"}}
	t.Cleanup(func() {
		newKafkaAdmin = func(ctx context.Context, a kafka.AuthConfig) (kafkaAdmin, error) { return &fakeKafkaAdmin{}, nil }
	})
	newKafkaAdmin = func(ctx context.Context, a kafka.AuthConfig) (kafkaAdmin, error) { return &fake, nil }
	outputFormat = "json"
	cmd := cmdACLList()
	cmd.SetArgs([]string{"--brokers", "b1:9096", "--sasl-username", "u", "--sasl-password", "p"})
	var execErr error
	out, _ := captureOutput(t, func() { execErr = cmd.Execute() })
	if execErr != nil {
		t.Fatalf("execute error: %v", execErr)
	}
	// JSON uses exported field names (no json tags)
	if !strings.Contains(out, "\"ResourceType\": \"topic\"") || !strings.Contains(out, "\"Principal\": \"User:alice\"") {
		t.Fatalf("unexpected ACL list json: %s", out)
	}
}

func TestACLDelete_JSON(t *testing.T) {
	var fake fakeKafkaAdmin
	fake.deleteACLsOut = []kafka.ACLEntry{{ResourceType: "topic", ResourceName: "foo", Principal: "User:alice", Operation: "read", Permission: "allow"}}
	t.Cleanup(func() {
		newKafkaAdmin = func(ctx context.Context, a kafka.AuthConfig) (kafkaAdmin, error) { return &fakeKafkaAdmin{}, nil }
	})
	newKafkaAdmin = func(ctx context.Context, a kafka.AuthConfig) (kafkaAdmin, error) { return &fake, nil }
	outputFormat = "json"
	cmd := cmdACLDelete()
	cmd.SetArgs([]string{"--brokers", "b1:9096", "--sasl-username", "u", "--sasl-password", "p"})
	var execErr error
	out, _ := captureOutput(t, func() { execErr = cmd.Execute() })
	if execErr != nil {
		t.Fatalf("execute error: %v", execErr)
	}
	if !strings.Contains(out, "\"ResourceType\": \"topic\"") {
		t.Fatalf("unexpected ACL delete json: %s", out)
	}
}

func TestGroup_List_JSON(t *testing.T) {
	var fake fakeKafkaAdmin
	fake.listGroupsOut = []string{"g1", "g2"}
	t.Cleanup(func() {
		newKafkaAdmin = func(ctx context.Context, a kafka.AuthConfig) (kafkaAdmin, error) { return &fakeKafkaAdmin{}, nil }
	})
	newKafkaAdmin = func(ctx context.Context, a kafka.AuthConfig) (kafkaAdmin, error) { return &fake, nil }
	outputFormat = "json"
	cmd := cmdGroupList()
	cmd.SetArgs([]string{"--brokers", "b1:9096", "--sasl-username", "u", "--sasl-password", "p"})
	out, _ := captureOutput(t, func() { _ = cmd.Execute() })
	if !strings.Contains(out, "\"g1\"") || !strings.Contains(out, "\"g2\"") {
		t.Fatalf("unexpected groups json: %s", out)
	}
}

func TestGroup_Describe_JSON(t *testing.T) {
	var fake fakeKafkaAdmin
	fake.describeOut = &kafka.ConsumerGroupDescription{GroupID: "g1", State: "Stable", Members: []kafka.GroupMember{{MemberID: "m1", ClientID: "c1", ClientHost: "h1"}}}
	t.Cleanup(func() {
		newKafkaAdmin = func(ctx context.Context, a kafka.AuthConfig) (kafkaAdmin, error) { return &fakeKafkaAdmin{}, nil }
	})
	newKafkaAdmin = func(ctx context.Context, a kafka.AuthConfig) (kafkaAdmin, error) { return &fake, nil }
	outputFormat = "json"
	cmd := cmdGroupDescribe()
	cmd.SetArgs([]string{"--brokers", "b1:9096", "--sasl-username", "u", "--sasl-password", "p", "--group-id", "g1"})
	var execErr error
	out, _ := captureOutput(t, func() { execErr = cmd.Execute() })
	if execErr != nil {
		t.Fatalf("execute error: %v", execErr)
	}
	if !strings.Contains(out, "\"GroupID\": \"g1\"") {
		t.Fatalf("unexpected describe json: %s", out)
	}
}

func TestGroup_Delete_JSON(t *testing.T) {
	var fake fakeKafkaAdmin
	fake.deleteGroupsRes = map[string]error{"g1": nil, "g2": nil}
	t.Cleanup(func() {
		newKafkaAdmin = func(ctx context.Context, a kafka.AuthConfig) (kafkaAdmin, error) { return &fakeKafkaAdmin{}, nil }
	})
	newKafkaAdmin = func(ctx context.Context, a kafka.AuthConfig) (kafkaAdmin, error) { return &fake, nil }
	outputFormat = "json"
	cmd := cmdGroupDelete()
	cmd.SetArgs([]string{"--brokers", "b1:9096", "--sasl-username", "u", "--sasl-password", "p", "--group-id", "g1", "--group-id", "g2"})
	var execErr error
	out, _ := captureOutput(t, func() { execErr = cmd.Execute() })
	if execErr != nil {
		t.Fatalf("execute error: %v", execErr)
	}
	if !strings.Contains(out, "g1") || !strings.Contains(out, "g2") {
		t.Fatalf("unexpected delete json: %s", out)
	}
}
