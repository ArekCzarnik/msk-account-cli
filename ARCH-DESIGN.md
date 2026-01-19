# Design Document: MSK Admin CLI

## Overview

The MSK Admin CLI (`msk-admin`) is a Go-based command-line application that provides a unified interface for managing Amazon MSK SCRAM credentials and Kafka resources. The tool bridges three distinct domains: AWS Secrets Manager for credential storage, AWS MSK API for cluster-secret association, and Kafka Admin APIs for ACL and consumer group management.

The application follows a modular architecture with clear separation between AWS operations, Kafka operations, configuration management, and output formatting. It uses the AWS SDK for Go v2 for AWS interactions and the IBM Sarama library for Kafka admin operations.

## Architecture

### High-Level Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                         CLI Layer                            │
│                    (Cobra Commands)                          │
└────────────┬────────────────────────────────┬───────────────┘
             │                                 │
             ▼                                 ▼
┌────────────────────────┐      ┌────────────────────────────┐
│   AWS Operations       │      │   Kafka Operations         │
│   - Secrets Manager    │      │   - Admin Client           │
│   - MSK API            │      │   - ACL Management         │
│                        │      │   - Consumer Groups        │
└────────────────────────┘      └────────────────────────────┘
             │                                 │
             ▼                                 ▼
┌────────────────────────┐      ┌────────────────────────────┐
│   AWS SDK v2           │      │   Sarama Library           │
└────────────────────────┘      └────────────────────────────┘
```

### Module Structure

```
msk-admin/
├── cmd/
│   └── msk-admin/
│       └── main.go              # Entry point, command registration
├── internal/
│   ├── aws/
│   │   ├── secrets.go           # Secrets Manager operations
│   │   └── msk.go               # MSK API operations
│   ├── kafka/
│   │   └── admin.go             # Kafka admin operations (ACLs, groups)
│   ├── config/
│   │   └── config.go            # Configuration, validation, shared types
│   └── output/
│       └── output.go            # Output formatting (table, JSON)
├── go.mod
├── go.sum
└── README.md
```

## Components and Interfaces

### 1. CLI Layer (cmd/msk-admin/main.go)

Uses the Cobra library to define commands and subcommands. Each command:
- Parses flags and validates required parameters
- Calls appropriate internal package functions
- Handles errors and formats output
- Returns appropriate exit codes

**Command Structure:**
- `account create` - Create SCRAM secret
- `account get` - Retrieve SCRAM secret
- `msk associate-secret` - Associate secret with cluster
- `msk disassociate-secret` - Disassociate secret from cluster
- `acl create` - Create Kafka ACL
- `acl list` - List Kafka ACLs
- `acl delete` - Delete Kafka ACLs
- `group list` - List consumer groups
- `group describe` - Describe consumer group
- `group delete` - Delete consumer groups

### 2. AWS Secrets Manager Module (internal/aws/secrets.go)

**Interface:**
```go
type SecretsManager interface {
    CreateSecret(ctx context.Context, params CreateSecretParams) (*CreateSecretResult, error)
    GetSecret(ctx context.Context, params GetSecretParams) (*GetSecretResult, error)
}

type CreateSecretParams struct {
    Region      string
    SecretName  string
    KMSKeyID    string
    Username    string
    Password    string
    Tags        map[string]string
}

type CreateSecretResult struct {
    ARN string
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
```

**Responsibilities:**
- Validate secret name prefix (AmazonMSK_)
- Validate KMS key ID is provided
- Construct JSON payload with username/password
- Create secret using AWS SDK v2
- Retrieve and parse secret JSON

### 3. AWS MSK Module (internal/aws/msk.go)

**Interface:**
```go
type MSKManager interface {
    AssociateSecrets(ctx context.Context, params AssociateSecretsParams) error
    DisassociateSecrets(ctx context.Context, params DisassociateSecretsParams) error
}

type AssociateSecretsParams struct {
    Region     string
    ClusterARN string
    SecretARNs []string
}

type DisassociateSecretsParams struct {
    Region     string
    ClusterARN string
    SecretARNs []string
}
```

**Responsibilities:**
- Call BatchAssociateScramSecret API
- Call BatchDisassociateScramSecret API
- Handle API responses and errors

### 4. Kafka Admin Module (internal/kafka/admin.go)

**Interface:**
```go
type KafkaAdmin interface {
    CreateACL(ctx context.Context, params CreateACLParams) error
    ListACLs(ctx context.Context, params ListACLsParams) ([]ACLEntry, error)
    DeleteACLs(ctx context.Context, params DeleteACLsParams) ([]ACLEntry, error)
    ListConsumerGroups(ctx context.Context) ([]string, error)
    DescribeConsumerGroup(ctx context.Context, groupID string) (*ConsumerGroupDescription, error)
    DeleteConsumerGroups(ctx context.Context, groupIDs []string) (map[string]error, error)
    Close() error
}

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

type ConsumerGroupDescription struct {
    GroupID   string
    State     string
    Members   []GroupMember
    Offsets   map[string]map[int32]OffsetInfo
}

type GroupMember struct {
    MemberID   string
    ClientID   string
    ClientHost string
}

type OffsetInfo struct {
    Offset int64
    Lag    int64
}
```

**Responsibilities:**
- Establish SASL_SSL connection to Kafka brokers
- Support SCRAM-SHA-256 and SCRAM-SHA-512 authentication
- Retrieve credentials from Secrets Manager if secret ARN provided
- Create, list, and delete ACLs using Sarama Admin API
- List, describe, and delete consumer groups using Sarama Admin API
- Manage connection lifecycle

### 5. Configuration Module (internal/config/config.go)

**Interface:**
```go
type KafkaConnectionConfig struct {
    Brokers        []string
    SASLUsername   string
    SASLPassword   string
    SecretARN      string
    Region         string
    SCRAMMechanism string // "sha256" or "sha512"
}

type Validator interface {
    ValidateSecretName(name string) error
    ValidateKMSKeyID(keyID string) error
    ValidateSecretPayload(username, password string) error
    ValidateResourceType(resourceType string) error
    ValidateResourcePattern(pattern string) error
    ValidateOperation(operation string) error
    ValidatePermission(permission string) error
}
```

**Responsibilities:**
- Define shared configuration structures
- Validate inputs according to MSK and Kafka requirements
- Provide validation functions for all user inputs
- Define constants for valid enum values

### 6. Output Module (internal/output/output.go)

**Interface:**
```go
type Formatter interface {
    FormatACLs(acls []ACLEntry, format string) (string, error)
    FormatConsumerGroups(groups []string, format string) (string, error)
    FormatConsumerGroupDescription(desc *ConsumerGroupDescription, format string) (string, error)
}
```

**Responsibilities:**
- Format data as tables using a table library (e.g., tablewriter)
- Format data as JSON
- Handle output flag (table or json)

## Data Models

### Secret Payload

The secret stored in AWS Secrets Manager follows this exact JSON structure:

```json
{
  "username": "alice",
  "password": "alice-secret"
}
```

Both fields are required and must be non-empty strings.

### ACL Entry

Represents a Kafka ACL with the following fields:
- **ResourceType**: topic, group, cluster, or transactionalId
- **ResourceName**: Name of the resource
- **ResourcePattern**: literal or prefixed
- **Principal**: Identity (e.g., "User:alice")
- **Host**: IP address or "*" for all hosts
- **Operation**: read, write, create, delete, alter, describe, etc.
- **Permission**: allow or deny

### Consumer Group Description

Contains detailed information about a consumer group:
- **GroupID**: Unique identifier for the group
- **State**: Current state (e.g., Stable, Empty, Dead)
- **Members**: List of active members with client information
- **Offsets**: Per-topic, per-partition offset and lag information

## Correct
ness Properties

*A property is a characteristic or behavior that should hold true across all valid executions of a system-essentially, a formal statement about what the system should do. Properties serve as the bridge between human-readable specifications and machine-verifiable correctness guarantees.*

### Property 1: Secret name prefix validation

*For any* secret name input, the validation function should accept names beginning with "AmazonMSK_" and reject all other names with a descriptive error message.

**Validates: Requirements 1.1, 11.1**

### Property 2: KMS key requirement validation

*For any* secret creation request, if the KMS key ID is empty or not provided, the validation should reject the request with an error explaining that customer-managed KMS keys are required.

**Validates: Requirements 1.2**

### Property 3: Secret payload round-trip

*For any* valid username and password pair, serializing them to the MSK secret JSON format and then deserializing should produce the original username and password values.

**Validates: Requirements 1.3, 4.5**

### Property 4: Non-empty credential validation

*For any* username and password pair where either field is empty or contains only whitespace, the validation should reject the input.

**Validates: Requirements 1.4**

### Property 5: Secret retrieval by identifier equivalence

*For any* secret that exists in Secrets Manager, retrieving it by ARN and retrieving it by name should return the same username and password values.

**Validates: Requirements 2.4**

### Property 6: Password display control

*For any* secret retrieval operation without the show-password flag, the output should contain the username but not contain the password value. When the show-password flag is provided, the output should contain both username and password.

**Validates: Requirements 2.2, 2.3, 12.2**

### Property 7: Multiple secret ARN handling

*For any* list of secret ARNs (1 to N), the associate and disassociate operations should accept and process all provided ARNs without truncation or loss.

**Validates: Requirements 3.2**

### Property 8: SCRAM mechanism configuration

*For any* authentication configuration, when the scram-mechanism flag is set to "sha256" or "sha512", the Kafka client configuration should use the corresponding SCRAM mechanism. When not specified, it should default to SCRAM-SHA-512.

**Validates: Requirements 4.2, 4.3**

### Property 9: Authentication credential alternatives

*For any* Kafka operation, providing either (username AND password) OR (secret ARN AND region) should result in a valid authentication configuration with credentials populated.

**Validates: Requirements 4.4**

### Property 10: ACL resource type validation

*For any* resource type input, the validation should accept exactly the values: "topic", "group", "cluster", "transactionalId" and reject all other values.

**Validates: Requirements 5.1**

### Property 11: ACL resource pattern validation

*For any* resource pattern input, the validation should accept "literal" and "prefixed", default to "literal" when not specified, and reject all other values.

**Validates: Requirements 5.2**

### Property 12: ACL operation validation

*For any* operation input, the validation should accept the values: "read", "write", "create", "delete", "alter", "describe" (and other valid Kafka operations) and reject invalid operation names.

**Validates: Requirements 5.3**

### Property 13: ACL permission validation

*For any* permission input, the validation should accept exactly "allow" and "deny" and reject all other values.

**Validates: Requirements 5.4**

### Property 14: ACL filtering correctness

*For any* collection of ACL entries and any filter criteria (resource type, resource name, principal, or operation), all ACLs returned by the filter function should match the specified criteria, and no matching ACLs should be excluded.

**Validates: Requirements 6.2, 7.1**

### Property 15: JSON output validity

*For any* data structure (ACLs, consumer groups, or consumer group descriptions) formatted as JSON, the output should be valid JSON that can be parsed back into an equivalent structure.

**Validates: Requirements 6.4, 8.2**

### Property 16: Multiple group ID handling

*For any* list of consumer group IDs (1 to N), the delete operation should accept and process all provided group IDs, returning results for each.

**Validates: Requirements 10.1, 10.3**

### Property 17: Error exit codes

*For any* operation that encounters an error (validation failure, API error, connection failure), the CLI tool should return a non-zero exit code.

**Validates: Requirements 11.4**

### Property 18: Sensitive data exclusion from logs

*For any* log output generated during operations, the output should not contain password values or complete secret strings.

**Validates: Requirements 12.1**

### Property 19: Alternative credential input methods

*For any* password value, whether provided via command-line flag, environment variable, or stdin, the resulting credential configuration should contain the same password value.

**Validates: Requirements 12.3**

## Error Handling

### Validation Errors

All input validation should occur before making any API calls. Validation errors should:
- Return immediately with descriptive error messages
- Include the specific validation rule that failed
- Suggest corrective actions when applicable
- Return exit code 1

**Validation Categories:**
1. **Secret Name Validation**: Check AmazonMSK_ prefix
2. **KMS Key Validation**: Ensure KMS key ID is provided and non-empty
3. **Credential Validation**: Verify username and password are non-empty
4. **Resource Type Validation**: Check against allowed Kafka resource types
5. **Pattern Validation**: Verify literal or prefixed
6. **Operation Validation**: Check against valid Kafka operations
7. **Permission Validation**: Verify allow or deny

### AWS API Errors

AWS SDK errors should be caught and translated into user-friendly messages:
- **Access Denied**: Indicate missing IAM permissions and suggest required policies
- **Resource Not Found**: Clearly state which resource (secret, cluster) was not found
- **Throttling**: Suggest retry with backoff
- **Network Errors**: Indicate connectivity issues and suggest checking network/VPC configuration

### Kafka API Errors

Kafka errors should be handled with context:
- **Authentication Failures**: Indicate SCRAM credential issues, suggest verifying username/password
- **Authorization Failures**: Indicate ACL issues, suggest checking user permissions
- **Connection Failures**: Indicate broker connectivity issues, suggest checking bootstrap brokers and network
- **Timeout Errors**: Indicate operation timeout, suggest checking cluster health

### Context Timeouts

All AWS and Kafka operations should use context with timeout:
- **AWS Operations**: 30-second timeout for API calls
- **Kafka Connection**: 10-second timeout for initial connection
- **Kafka Operations**: 30-second timeout for admin operations

## Testing Strategy

The MSK Admin CLI will employ a dual testing approach combining unit tests and property-based tests to ensure comprehensive correctness validation.

### Unit Testing

Unit tests will verify specific behaviors and integration points:

**Validation Functions:**
- Test each validation function with valid and invalid inputs
- Verify error messages are descriptive and accurate
- Test edge cases (empty strings, special characters, boundary values)

**Output Formatting:**
- Test table formatting with sample data
- Test JSON formatting produces valid, parseable JSON
- Verify output respects the --output flag

**Configuration Building:**
- Test Kafka connection configuration with different auth methods
- Test AWS client configuration with different regions
- Verify default values are applied correctly

**Error Handling:**
- Test that validation errors return appropriate exit codes
- Test that API errors are translated to user-friendly messages
- Verify sensitive data is redacted in error messages

### Property-Based Testing

Property-based tests will verify universal correctness properties across many randomly generated inputs. The tool will use the **gopter** library for property-based testing in Go.

**Configuration:**
- Each property test should run a minimum of 100 iterations
- Tests should use appropriate generators for each data type
- Generators should produce both valid and invalid inputs to test validation

**Property Test Requirements:**
- Each property-based test MUST be tagged with a comment explicitly referencing the correctness property from this design document
- Tag format: `// Feature: msk-admin-cli, Property {number}: {property_text}`
- Each correctness property MUST be implemented by a SINGLE property-based test
- Tests should be placed in `_test.go` files alongside the code they test

**Property Test Coverage:**

1. **Validation Properties** (Properties 1, 2, 4, 10-13):
   - Generate random strings and verify validation accepts/rejects correctly
   - Test boundary cases (empty, whitespace, special characters)

2. **Round-Trip Properties** (Property 3):
   - Generate random username/password pairs
   - Verify serialize → deserialize produces original values

3. **Equivalence Properties** (Properties 5, 9):
   - Generate random valid inputs
   - Verify different input methods produce equivalent results

4. **Display Properties** (Property 6):
   - Generate random secrets
   - Verify password presence/absence based on flag

5. **Collection Handling Properties** (Properties 7, 16):
   - Generate lists of varying sizes (0 to 100 items)
   - Verify all items are processed correctly

6. **Filtering Properties** (Property 14):
   - Generate random ACL collections and filter criteria
   - Verify all returned items match filter and no matches are excluded

7. **Format Properties** (Property 15):
   - Generate random data structures
   - Verify JSON output is valid and round-trips correctly

8. **Error Properties** (Property 17):
   - Generate error conditions
   - Verify non-zero exit codes

9. **Security Properties** (Property 18):
   - Generate random operations with credentials
   - Verify logs don't contain sensitive data

**Test Organization:**
```
internal/
├── aws/
│   ├── secrets_test.go          # Unit tests for secrets operations
│   ├── secrets_property_test.go # Property tests for secrets
│   └── msk_test.go              # Unit tests for MSK operations
├── kafka/
│   ├── admin_test.go            # Unit tests for Kafka admin
│   └── admin_property_test.go   # Property tests for Kafka admin
├── config/
│   ├── config_test.go           # Unit tests for configuration
│   └── validation_property_test.go # Property tests for validation
└── output/
    ├── output_test.go           # Unit tests for output formatting
    └── output_property_test.go  # Property tests for formatting
```

### Integration Testing

While not part of the automated test suite, integration testing should be performed manually:
- Test against a real MSK cluster in a development AWS account
- Verify end-to-end workflows (create secret → associate → create ACL → verify access)
- Test with different SCRAM mechanisms
- Verify ACL and consumer group operations against live Kafka cluster

## Implementation Notes

### Dependencies

**AWS SDK for Go v2:**
```go
github.com/aws/aws-sdk-go-v2/config
github.com/aws/aws-sdk-go-v2/service/secretsmanager
github.com/aws/aws-sdk-go-v2/service/kafka
```

**Kafka Client:**
```go
github.com/IBM/sarama  // Supports admin operations for ACLs and consumer groups
```

**CLI Framework:**
```go
github.com/spf13/cobra  // Command-line interface framework
```

**Output Formatting:**
```go
github.com/olekukonko/tablewriter  // Table formatting
```

**Property-Based Testing:**
```go
github.com/leanovate/gopter  // Property-based testing framework
```

### Security Considerations

1. **Credential Handling:**
   - Never log passwords or secret strings
   - Redact sensitive data in error messages
   - Support reading passwords from stdin to avoid shell history
   - Clear sensitive data from memory when no longer needed

2. **TLS Configuration:**
   - Always use TLS for Kafka connections (SASL_SSL)
   - Use system certificate pool for TLS verification
   - Do not allow insecure TLS configurations

3. **IAM Permissions:**
   - Document required IAM permissions in README
   - Use least-privilege principle
   - Support IAM roles for EC2/ECS environments

4. **Input Validation:**
   - Validate all user inputs before processing
   - Sanitize inputs to prevent injection attacks
   - Use parameterized API calls (SDK handles this)

### Performance Considerations

1. **Connection Pooling:**
   - Reuse Kafka admin client for multiple operations in same command
   - Close connections properly to avoid resource leaks

2. **Batch Operations:**
   - Use batch APIs when available (BatchAssociateScramSecret)
   - Process multiple ACLs or groups in single API call when possible

3. **Timeouts:**
   - Set reasonable timeouts for all operations
   - Allow users to configure timeouts via flags if needed

4. **Output Buffering:**
   - Buffer output for large result sets
   - Stream results for very large lists if memory becomes a concern

### Extensibility

The design supports future enhancements:
- Additional Kafka admin operations (topics, configs)
- Support for other authentication mechanisms (mTLS)
- Interactive mode for complex workflows
- Configuration file support for common parameters
- Shell completion for commands and flags
