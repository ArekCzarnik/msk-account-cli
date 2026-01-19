package kafka

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"errors"
	"fmt"
	"hash"
	"strings"
	"time"

	"github.com/IBM/sarama"
	"github.com/czarnik/msk-account-cli/internal/logging"
	xdg "github.com/xdg-go/scram"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// AuthConfig contains Kafka connection info.
type AuthConfig struct {
	Brokers        []string
	SASLUsername   string
	SASLPassword   string
	SCRAMMechanism string
}

func (a AuthConfig) Validate() error {
	if len(a.Brokers) == 0 || strings.TrimSpace(a.Brokers[0]) == "" {
		return errors.New("--brokers is required")
	}
	if strings.TrimSpace(a.SASLUsername) == "" || strings.TrimSpace(a.SASLPassword) == "" {
		return errors.New("SASL credentials are required (either via --sasl-username/--sasl-password or --secret-arn)")
	}
	mech := strings.ToLower(strings.TrimSpace(a.SCRAMMechanism))
	if mech == "" {
		mech = "sha512"
	}
	if mech != "sha256" && mech != "sha512" {
		return fmt.Errorf("invalid scram-mechanism %q (must be sha256|sha512)", a.SCRAMMechanism)
	}
	return nil
}

// admin wraps sarama.ClusterAdmin
type admin struct {
	ca sarama.ClusterAdmin
}

// NewAdmin connects and returns an Admin client.
func NewAdmin(ctx context.Context, a AuthConfig) (*admin, error) {
	tctx, span := otel.Tracer("github.com/czarnik/msk-account-cli/kafka").Start(ctx, "kafka.admin.connect",
		trace.WithAttributes(
			attribute.String("kafka.brokers", strings.Join(a.Brokers, ",")),
			attribute.String("kafka.scram", a.SCRAMMechanism),
			attribute.String("kafka.username", a.SASLUsername),
		),
	)
	defer span.End()
	_ = tctx
	if logging.L != nil {
		logging.L.Info("kafka.admin.connect",
			"brokers", strings.Join(a.Brokers, ","),
			"username", a.SASLUsername,
			"scram", a.SCRAMMechanism,
		)
	}
	cfg := sarama.NewConfig()
	cfg.Version = sarama.V2_5_0_0 // MSK typically supports >= 2.x; adjust if needed
	cfg.Net.TLS.Enable = true
	cfg.Net.SASL.Enable = true
	cfg.Net.SASL.User = a.SASLUsername
	cfg.Net.SASL.Password = a.SASLPassword
	cfg.Net.SASL.Handshake = true
	cfg.Net.DialTimeout = 10 * time.Second
	cfg.Net.ReadTimeout = 30 * time.Second
	cfg.Net.WriteTimeout = 30 * time.Second
	cfg.Admin.Timeout = 30 * time.Second

	mech := strings.ToLower(strings.TrimSpace(a.SCRAMMechanism))
	if mech == "" {
		mech = "sha512"
	}
	if mech == "sha256" {
		cfg.Net.SASL.Mechanism = sarama.SASLTypeSCRAMSHA256
		cfg.Net.SASL.SCRAMClientGeneratorFunc = func() sarama.SCRAMClient {
			return &xdgSCRAMClient{hashGeneratorFcn: sha256.New}
		}
	} else {
		cfg.Net.SASL.Mechanism = sarama.SASLTypeSCRAMSHA512
		cfg.Net.SASL.SCRAMClientGeneratorFunc = func() sarama.SCRAMClient {
			return &xdgSCRAMClient{hashGeneratorFcn: sha512.New}
		}
	}

	ca, err := sarama.NewClusterAdmin(a.Brokers, cfg)
	if err != nil {
		if logging.L != nil {
			logging.L.Error("kafka.admin.connect.error", "error", err)
		}
		return nil, err
	}
	if logging.L != nil {
		logging.L.Info("kafka.admin.connect.ok")
	}
	return &admin{ca: ca}, nil
}

func (a *admin) Close() error { return a.ca.Close() }

// ACL operations

type CreateACLParams struct {
	ResourceType    string
	ResourceName    string
	ResourcePattern string
	Principal       string
	Host            string
	Operation       string
	Permission      string
}

type ACLEntry struct {
	ResourceType    string
	ResourceName    string
	ResourcePattern string
	Principal       string
	Host            string
	Operation       string
	Permission      string
}

type ListACLsParams struct {
	ResourceType string
	ResourceName string
	Principal    string
	Operation    string
}

type DeleteACLsParams struct {
	ResourceType    string
	ResourceName    string
	ResourcePattern string
	Principal       string
	Host            string
	Operation       string
	Permission      string
}

func (a *admin) CreateACL(ctx context.Context, p CreateACLParams) error {
	tctx, span := otel.Tracer("github.com/czarnik/msk-account-cli/kafka").Start(ctx, "kafka.acl.create",
		trace.WithAttributes(
			attribute.String("resource.type", p.ResourceType),
			attribute.String("resource.name", p.ResourceName),
			attribute.String("pattern", p.ResourcePattern),
			attribute.String("principal", p.Principal),
			attribute.String("host", p.Host),
			attribute.String("operation", p.Operation),
			attribute.String("permission", p.Permission),
		),
	)
	defer span.End()
	_ = tctx
	if logging.L != nil {
		logging.L.Info("kafka.acl.create",
			"resource_type", p.ResourceType,
			"resource_name", p.ResourceName,
			"pattern", p.ResourcePattern,
			"principal", p.Principal,
			"host", p.Host,
			"operation", p.Operation,
			"permission", p.Permission,
		)
	}
	resType, err := toResourceType(p.ResourceType)
	if err != nil {
		return err
	}
	pattType, err := toPatternType(p.ResourcePattern)
	if err != nil {
		return err
	}
	op, err := toOperation(p.Operation)
	if err != nil {
		return err
	}
	perm, err := toPermission(p.Permission)
	if err != nil {
		return err
	}

	res := sarama.Resource{ResourceType: resType, ResourceName: p.ResourceName, ResourcePatternType: pattType}
	acl := sarama.Acl{Principal: p.Principal, Host: emptyOr(p.Host, "*"), Operation: op, PermissionType: perm}
	err = a.ca.CreateACL(res, acl)
	if err != nil {
		if logging.L != nil {
			logging.L.Error("kafka.acl.create.error", "error", err)
		}
		return err
	}
	if logging.L != nil {
		logging.L.Info("kafka.acl.create.ok")
	}
	return nil
}

func (a *admin) ListACLs(ctx context.Context, p ListACLsParams) ([]ACLEntry, error) {
	tctx, span := otel.Tracer("github.com/czarnik/msk-account-cli/kafka").Start(ctx, "kafka.acl.list",
		trace.WithAttributes(
			attribute.String("resource.type", p.ResourceType),
			attribute.String("resource.name", p.ResourceName),
			attribute.String("principal", p.Principal),
			attribute.String("operation", p.Operation),
		),
	)
	defer span.End()
	_ = tctx
	if logging.L != nil {
		logging.L.Info("kafka.acl.list",
			"resource_type", p.ResourceType,
			"resource_name", p.ResourceName,
			"principal", p.Principal,
			"operation", p.Operation,
		)
	}
	filter := sarama.AclFilter{}
	if p.ResourceType != "" {
		rt, err := toResourceType(p.ResourceType)
		if err != nil {
			return nil, err
		}
		filter.ResourceType = rt
	}
	if p.ResourceName != "" {
		rn := p.ResourceName
		filter.ResourceName = &rn
	}
	if p.Principal != "" {
		pr := p.Principal
		filter.Principal = &pr
	}
	if p.Operation != "" {
		op, err := toOperation(p.Operation)
		if err != nil {
			return nil, err
		}
		filter.Operation = op
	}
	res, err := a.ca.ListAcls(filter)
	if err != nil {
		if logging.L != nil {
			logging.L.Error("kafka.acl.list.error", "error", err)
		}
		return nil, err
	}
	var out []ACLEntry
	for _, ra := range res {
		for _, acl := range ra.Acls {
			out = append(out, ACLEntry{
				ResourceType:    fromResourceType(ra.Resource.ResourceType),
				ResourceName:    ra.Resource.ResourceName,
				ResourcePattern: fromPatternType(ra.Resource.ResourcePatternType),
				Principal:       acl.Principal,
				Host:            acl.Host,
				Operation:       fromOperation(acl.Operation),
				Permission:      fromPermission(acl.PermissionType),
			})
		}
	}
	if logging.L != nil {
		logging.L.Info("kafka.acl.list.ok", "count", len(out))
	}
	return out, nil
}

func (a *admin) DeleteACLs(ctx context.Context, p DeleteACLsParams) ([]ACLEntry, error) {
	tctx, span := otel.Tracer("github.com/czarnik/msk-account-cli/kafka").Start(ctx, "kafka.acl.delete",
		trace.WithAttributes(
			attribute.String("resource.type", p.ResourceType),
			attribute.String("resource.name", p.ResourceName),
			attribute.String("pattern", p.ResourcePattern),
			attribute.String("principal", p.Principal),
			attribute.String("host", p.Host),
			attribute.String("operation", p.Operation),
			attribute.String("permission", p.Permission),
		),
	)
	defer span.End()
	_ = tctx
	if logging.L != nil {
		logging.L.Info("kafka.acl.delete",
			"resource_type", p.ResourceType,
			"resource_name", p.ResourceName,
			"pattern", p.ResourcePattern,
			"principal", p.Principal,
			"host", p.Host,
			"operation", p.Operation,
			"permission", p.Permission,
		)
	}
	filter := sarama.AclFilter{}
	if p.ResourceType != "" {
		rt, err := toResourceType(p.ResourceType)
		if err != nil {
			return nil, err
		}
		filter.ResourceType = rt
	}
	if p.ResourceName != "" {
		rn := p.ResourceName
		filter.ResourceName = &rn
	}
	if p.ResourcePattern != "" {
		pt, err := toPatternType(p.ResourcePattern)
		if err != nil {
			return nil, err
		}
		filter.ResourcePatternTypeFilter = pt
	}
	if p.Principal != "" {
		pr := p.Principal
		filter.Principal = &pr
	}
	if p.Host != "" {
		h := p.Host
		filter.Host = &h
	}
	if p.Operation != "" {
		op, err := toOperation(p.Operation)
		if err != nil {
			return nil, err
		}
		filter.Operation = op
	}
	if p.Permission != "" {
		pe, err := toPermission(p.Permission)
		if err != nil {
			return nil, err
		}
		filter.PermissionType = pe
	}

	matches, err := a.ca.DeleteACL(filter, false)
	if err != nil {
		if logging.L != nil {
			logging.L.Error("kafka.acl.delete.error", "error", err)
		}
		return nil, err
	}
	var out []ACLEntry
	for _, m := range matches {
		out = append(out, ACLEntry{
			ResourceType:    fromResourceType(m.Resource.ResourceType),
			ResourceName:    m.Resource.ResourceName,
			ResourcePattern: fromPatternType(m.Resource.ResourcePatternType),
			Principal:       m.Acl.Principal,
			Host:            m.Acl.Host,
			Operation:       fromOperation(m.Acl.Operation),
			Permission:      fromPermission(m.Acl.PermissionType),
		})
	}
	if logging.L != nil {
		logging.L.Info("kafka.acl.delete.ok", "count", len(out))
	}
	return out, nil
}

// Consumer groups

func (a *admin) ListConsumerGroups(ctx context.Context) ([]string, error) {
	tctx, span := otel.Tracer("github.com/czarnik/msk-account-cli/kafka").Start(ctx, "kafka.group.list")
	defer span.End()
	_ = tctx
	if logging.L != nil {
		logging.L.Info("kafka.group.list")
	}
	m, err := a.ca.ListConsumerGroups()
	if err != nil {
		if logging.L != nil {
			logging.L.Error("kafka.group.list.error", "error", err)
		}
		return nil, err
	}
	out := make([]string, 0, len(m))
	for g := range m {
		out = append(out, g)
	}
	if logging.L != nil {
		logging.L.Info("kafka.group.list.ok", "count", len(out))
	}
	return out, nil
}

type GroupMember struct {
	MemberID   string
	ClientID   string
	ClientHost string
}

type ConsumerGroupDescription struct {
	GroupID string
	State   string
	Members []GroupMember
}

func (a *admin) DescribeConsumerGroup(ctx context.Context, groupID string) (*ConsumerGroupDescription, error) {
	tctx, span := otel.Tracer("github.com/czarnik/msk-account-cli/kafka").Start(ctx, "kafka.group.describe",
		trace.WithAttributes(attribute.String("group.id", groupID)))
	defer span.End()
	_ = tctx
	if logging.L != nil {
		logging.L.Info("kafka.group.describe", "group_id", groupID)
	}
	descs, err := a.ca.DescribeConsumerGroups([]string{groupID})
	if err != nil {
		if logging.L != nil {
			logging.L.Error("kafka.group.describe.error", "error", err)
		}
		return nil, err
	}
	if len(descs) == 0 {
		return nil, fmt.Errorf("group %s not found", groupID)
	}
	d := descs[0]
	out := &ConsumerGroupDescription{GroupID: d.GroupId, State: string(d.State)}
	for _, m := range d.Members {
		out.Members = append(out.Members, GroupMember{MemberID: m.MemberId, ClientID: m.ClientId, ClientHost: m.ClientHost})
	}
	if logging.L != nil {
		logging.L.Info("kafka.group.describe.ok", "members", len(out.Members), "state", out.State)
	}
	return out, nil
}

func (a *admin) DeleteConsumerGroups(ctx context.Context, groupIDs []string) (map[string]error, error) {
	if len(groupIDs) == 0 {
		return nil, errors.New("at least one group-id must be provided")
	}
	tctx, span := otel.Tracer("github.com/czarnik/msk-account-cli/kafka").Start(ctx, "kafka.group.delete",
		trace.WithAttributes(attribute.Int("group.count", len(groupIDs))))
	defer span.End()
	_ = tctx
	if logging.L != nil {
		logging.L.Info("kafka.group.delete", "count", len(groupIDs))
	}
	res := make(map[string]error, len(groupIDs))
	for _, g := range groupIDs {
		err := a.ca.DeleteConsumerGroup(g)
		res[g] = err
	}
	if logging.L != nil {
		logging.L.Info("kafka.group.delete.done")
	}
	return res, nil
}

// mapping helpers

func toResourceType(s string) (sarama.AclResourceType, error) {
	switch strings.ToLower(s) {
	case "topic":
		return sarama.AclResourceTopic, nil
	case "group":
		return sarama.AclResourceGroup, nil
	case "cluster":
		return sarama.AclResourceCluster, nil
	case "transactionalid":
		return sarama.AclResourceTransactionalID, nil
	}
	return 0, fmt.Errorf("unknown resource type %q", s)
}

func fromResourceType(t sarama.AclResourceType) string {
	switch t {
	case sarama.AclResourceTopic:
		return "topic"
	case sarama.AclResourceGroup:
		return "group"
	case sarama.AclResourceCluster:
		return "cluster"
	case sarama.AclResourceTransactionalID:
		return "transactionalId"
	default:
		return fmt.Sprintf("%d", t)
	}
}

func toPatternType(s string) (sarama.AclResourcePatternType, error) {
	if s == "" {
		return sarama.AclPatternLiteral, nil
	}
	switch strings.ToLower(s) {
	case "literal":
		return sarama.AclPatternLiteral, nil
	case "prefixed":
		return sarama.AclPatternPrefixed, nil
	}
	return 0, fmt.Errorf("unknown pattern type %q", s)
}

func fromPatternType(p sarama.AclResourcePatternType) string {
	switch p {
	case sarama.AclPatternLiteral:
		return "literal"
	case sarama.AclPatternPrefixed:
		return "prefixed"
	default:
		return fmt.Sprintf("%d", p)
	}
}

func toOperation(s string) (sarama.AclOperation, error) {
	switch strings.ToLower(s) {
	case "read":
		return sarama.AclOperationRead, nil
	case "write":
		return sarama.AclOperationWrite, nil
	case "create":
		return sarama.AclOperationCreate, nil
	case "delete":
		return sarama.AclOperationDelete, nil
	case "alter":
		return sarama.AclOperationAlter, nil
	case "describe":
		return sarama.AclOperationDescribe, nil
	case "all":
		return sarama.AclOperationAll, nil
	}
	return 0, fmt.Errorf("unknown operation %q", s)
}

func fromOperation(o sarama.AclOperation) string {
	switch o {
	case sarama.AclOperationRead:
		return "read"
	case sarama.AclOperationWrite:
		return "write"
	case sarama.AclOperationCreate:
		return "create"
	case sarama.AclOperationDelete:
		return "delete"
	case sarama.AclOperationAlter:
		return "alter"
	case sarama.AclOperationDescribe:
		return "describe"
	case sarama.AclOperationAll:
		return "all"
	default:
		return fmt.Sprintf("%d", o)
	}
}

func toPermission(s string) (sarama.AclPermissionType, error) {
	switch strings.ToLower(s) {
	case "allow":
		return sarama.AclPermissionAllow, nil
	case "deny":
		return sarama.AclPermissionDeny, nil
	}
	return 0, fmt.Errorf("unknown permission %q", s)
}

func fromPermission(p sarama.AclPermissionType) string {
	switch p {
	case sarama.AclPermissionAllow:
		return "allow"
	case sarama.AclPermissionDeny:
		return "deny"
	default:
		return fmt.Sprintf("%d", p)
	}
}

func emptyOr(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

// SCRAM client using xdg-go/scram
type xdgSCRAMClient struct {
	*xdg.Client
	*xdg.ClientConversation
	hashGeneratorFcn func() hash.Hash
}

func (x *xdgSCRAMClient) Begin(userName, password, authzID string) (err error) {
	// Map the selected hash generator function to xdg-go/scram HashGeneratorFcn
	var gen xdg.HashGeneratorFcn
	// Compare by producing a hash to decide which one is intended
	// Use pointer equivalence via function identity is not supported; decide by length
	// Explicitly choose based on known funcs from crypto packages
	if fmt.Sprintf("%T", x.hashGeneratorFcn()) == fmt.Sprintf("%T", sha256.New()) {
		gen = xdg.SHA256
	} else {
		gen = xdg.SHA512
	}
	c, err := gen.NewClient(userName, password, authzID)
	if err != nil {
		return err
	}
	x.Client = c
	x.ClientConversation = c.NewConversation()
	return nil
}

func (x *xdgSCRAMClient) Step(challenge string) (response string, err error) {
	return x.ClientConversation.Step(challenge)
}

func (x *xdgSCRAMClient) Done() bool { return x.ClientConversation.Done() }
