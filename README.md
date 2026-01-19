## msk-account-cli

![msk-account-cli.png](docs/msk-account-cli.png)

is an application that uses the AWS SDK for Go v2 to manage Amazon MSK SCRAM accounts stored in AWS Secrets Manager and to administer Kafka ACLs and consumer group IDs on an MSK cluster.

## Goal
Implement a CLI tool named `msk-account-cli` that can:
1) Create and retrieve SCRAM credentials in AWS Secrets Manager following MSK requirements.
2) Associate (and optionally disassociate) the created secret with an MSK cluster using the MSK API.
3) Connect to the Kafka cluster (bootstrap brokers) and manage:
    - ACLs (create/list/delete)
    - Consumer groups (list/describe/delete) a.k.a. "manage Group-ID"

## Requirements (Secrets Manager + MSK)
When creating a secret for MSK SCRAM:
- Secret name MUST begin with prefix: `AmazonMSK_`
- Secret MUST use a customer-managed KMS key (do NOT allow default KMS key).
- Secret payload MUST be JSON plaintext in the exact format:
  {
  "username": "alice",
  "password": "alice-secret"
  }
- After creating the secret, output the secret ARN.
- Provide a command to associate the secret with an MSK cluster via `BatchAssociateScramSecret`.
- Add guardrails:
    - If `kmsKeyId` is missing, fail with a clear error explaining that the default KMS key cannot be used with MSK.
    - Validate secret name prefix.
    - Validate JSON structure has non-empty username/password.

## Kafka connectivity requirements
- Use SCRAM authentication to connect to brokers.
- Use a Go Kafka client library that supports Admin APIs for ACL and consumer group operations.
  Prefer `github.com/IBM/sarama` (supports admin operations for ACLs and consumer groups).
- Connection inputs:
    - `--brokers` comma-separated bootstrap brokers
    - `--sasl-username`, `--sasl-password` OR a `--secret-arn` + `--region` that fetches credentials from Secrets Manager.
- TLS enabled by default (SASL_SSL).
- Support the most common SCRAM mechanism:
    - Default SCRAM-SHA-512, but allow `--scram-mechanism` = `sha256|sha512`

## CLI UX
Implement with cobra OR standard library flag parsing (cobra preferred). Provide subcommands:

### Secrets / Accounts
- `msk-account-cli account create`
  Flags:
    - `--region`
    - `--secret-name` (must start with AmazonMSK\_)
    - `--kms-key-id` (required; key ID or ARN; do NOT accept alias)
    - `--username`
    - `--password`
    - `--tags` (optional key=value repeated)
      Behavior:
    - Create secret in Secrets Manager with SecretString JSON and KmsKeyId set.
    - Print: secret ARN.

- `msk-account-cli account get`
  Flags:
    - `--region`
    - `--secret-arn` (or --secret-name)
      Behavior:
    - Retrieve and print username (never print password unless `--show-password` is set).

### MSK association
- `msk-account-cli msk associate-secret`
  Flags:
    - `--region`
    - `--cluster-arn`
    - `--secret-arn` (repeatable: allow multiple)
      Behavior:
    - Call MSK `BatchAssociateScramSecret` and print result.

- `msk-account-cli msk disassociate-secret` (optional but nice)
  Flags:
    - `--region`
    - `--cluster-arn`
    - `--secret-arn` (repeatable)
      Behavior:
    - Call MSK `BatchDisassociateScramSecret` and print result.

### ACL management
- `msk-account-cli acl create`
  Flags:
    - `--brokers`
    - Auth flags: either `--secret-arn + --region` OR `--sasl-username/--sasl-password`
    - `--resource-type` (topic|group|cluster|transactionalId)
    - `--resource-name`
    - `--resource-pattern` (literal|prefixed) default literal
    - `--principal` (e.g. "User:alice")
    - `--host` default "*"
    - `--operation` (read|write|create|delete|alter|describe|... include common Kafka ops)
    - `--permission` (allow|deny)
      Behavior:
    - Create ACL using Sarama Admin API.
    - Print created entry.

- `msk-account-cli acl list`
  Flags:
    - same auth/brokers
    - optional filters: resource-type/name, principal, operation
      Behavior:
    - List matching ACLs, print as table.

- `msk-account-cli acl delete`
  Flags:
    - same as list/create, but used as filter for deletions
      Behavior:
    - Delete matching ACLs, print what was removed.

### Consumer group (Group-ID) management
- `msk-account-cli group list`
  Flags: brokers + auth
  Behavior: list consumer groups.

- `msk-account-cli group describe`
  Flags: brokers + auth + `--group-id`
  Behavior: describe group (members, state, assignments, lag if available).

- `msk-account-cli group delete`
  Flags: brokers + auth + `--group-id` (repeatable)
  Behavior: delete groups, print results.

## Project structure
Generate a clean module layout:
- /cmd/msk-account-cli/main.go
- /internal/aws/secrets.go  (create/get secret)
- /internal/aws/msk.go      (associate/disassociate)
- /internal/kafka/admin.go  (sarama admin setup, acl/group ops)
- /internal/config/config.go (shared config, validation)
- /internal/output/output.go (table/json output; support `--output json|table`)
- README.md with usage examples and required IAM permissions.

## AWS SDK v2 usage details
- Use `config.LoadDefaultConfig` with region.
- Secrets Manager client: `secretsmanager.NewFromConfig(cfg)`
    - CreateSecret: set Name, SecretString, KmsKeyId, Tags
    - GetSecretValue: retrieve SecretString
- MSK client: `kafka.NewFromConfig(cfg)` (service is `kafka`)
    - BatchAssociateScramSecret
    - BatchDisassociateScramSecret

## Security / best practices
- Never log passwords or full secret string.
- Support reading password via stdin or env var if `--password` not provided (optional).
- Return non-zero exit codes on failures.
- Provide helpful error messages (prefix validation, missing kms key id, auth missing, etc.)
- Context timeouts for AWS and Kafka calls.

## Logging
All CLI actions are written as JSON logs to the `logs/` directory, in files named `msk-admin-YYYYMMDD.log`.

- Sensitive values (e.g., `--password`, `--sasl-password`, tokens, secrets) are automatically masked.
- The logger records command invocations (command path and flags), AWS/MSK operations, and success/error outcomes.
- Example entry:
  `{ "time": "...", "level": "INFO", "msg": "invoke", "cmd": "msk-admin account create", "secret-name": "AmazonMSK_example", "password": "********" }`


## Documentation snippet to embed into README
Explain the MSK secret requirements:
- "Other types of secrets" conceptually (we just create plaintext JSON)
- Name prefix `AmazonMSK\_`
- MUST use a customer-managed KMS key (default key not allowed)
- Format for username/password JSON
- After creation, associate secret to cluster via BatchAssociateScramSecret
  Also mention: if using AWS CLI, kms-key-id should be key ID/ARN not alias (our tool enforces this).

## MSK Admin CLI Usage

- Binary: `bin/msk-account-cli` (built with `make build`)
- Output: `--output table|json` (default: `table`)

GUI Mode (TUI)

- Start interactive view with `tview`
  `bin/msk-account-cli gui`
    - Left: menu (Accounts, MSK, ACL, Groups)
    - Right: result table/details
    - Input masks ask for the same parameters as the respective commands (e.g. `--region`, `--brokers`, auth, etc.).
    - Key `q` exits GUI mode.

Secrets / Accounts

- Create secret
  ~~~
  bin/msk-account-cli account create --region eu-central-1 --secret-name AmazonMSK_alice --kms-key-id arn:aws:kms:eu-central-1:111122223333:key/abcd-... --username alice --password 'S3cretP@ss' --tags env=dev --tags owner=platform
  ~~~

- Create secret and auto-create a KMS key
  ~~~
  bin/msk-account-cli account create --region eu-central-1 --secret-name AmazonMSK_alice --create-kms-key --kms-key-description "MSK SCRAM secrets" --username alice --password 'S3cretP@ss'
  ~~~

- Get secret (username only)
  ~~~
  bin/msk-account-cli account get --region eu-central-1 --secret-arn arn:aws:secretsmanager:eu-central-1:111122223333:secret:AmazonMSK_alice-XXXX
  ~~~

- Get secret (show password)
  ~~~
  bin/msk-account-cli account get --region eu-central-1 --secret-name AmazonMSK_alice --show-password
  ~~~

- List all AmazonMSK\_ accounts (with ARN)
  ~~~
  bin/msk-account-cli account list --region eu-central-1
  ~~~

- Delete secret (30d recovery)
  ~~~
  bin/msk-account-cli account delete --region eu-central-1 --secret-name AmazonMSK_alice
  ~~~

- Delete secret immediately (no recovery, irreversible)
  ~~~
  bin/msk-account-cli account delete --region eu-central-1 --secret-name AmazonMSK_alice --force
  ~~~

- Delete secret and schedule KMS key deletion in 7 days
  ~~~
  bin/msk-account-cli account delete --region eu-central-1 --secret-name AmazonMSK_alice --delete-kms-key --kms-pending-window-days 7
  ~~~

MSK association

- Associate secret(s) to cluster
  ~~~
  bin/msk-account-cli msk associate-secret --region eu-central-1 --cluster-arn arn:aws:kafka:eu-central-1:111122223333:cluster/dev/abcd-... --secret-arn arn:aws:secretsmanager:eu-central-1:111122223333:secret:AmazonMSK_alice-XXXX
  ~~~

- Disassociate secret(s)
  ~~~
  bin/msk-account-cli msk disassociate-secret --region eu-central-1 --cluster-arn arn:aws:kafka:eu-central-1:111122223333:cluster/dev/abcd-... --secret-arn arn:aws:secretsmanager:eu-central-1:111122223333:secret:AmazonMSK_alice-XXXX
  ~~~

MSK Cluster Listing

- List all MSK clusters (Name + ARN)
  ~~~
  bin/msk-account-cli msk list-clusters --region eu-central-1
  ~~~

- Filter by name prefix
  ~~~
  bin/msk-account-cli msk list-clusters --region eu-central-1 --name-prefix dev-
  ~~~

- Additional columns in table (state/type)
  ~~~
  bin/msk-account-cli msk list-clusters --region eu-central-1 --columns name,arn,state,type
  ~~~

- JSON contains additional fields automatically (state, type)
  ~~~
  bin/msk-account-cli msk list-clusters --region eu-central-1 --output json
  ~~~

Kafka Broker Listing

- Brokers of a cluster (ID and endpoints)
  ~~~
  bin/msk-account-cli msk list-brokers --region eu-central-1 --cluster-arn arn:aws:kafka:eu-central-1:111122223333:cluster/dev/abcd-...
  ~~~

ACL management

- Create ACL (allow alice to read topic foo)
  ~~~
  bin/msk-account-cli acl create --brokers b-1.example.kafka.amazonaws.com:9096,b-2.example.kafka.amazonaws.com:9096 --secret-arn arn:aws:secretsmanager:eu-central-1:111122223333:secret:AmazonMSK_alice-XXXX --region eu-central-1 --resource-type topic --resource-name foo --principal User:alice --operation read --permission allow
  ~~~

- List ACLs for topic foo
  ~~~
  bin/msk-account-cli acl list --brokers <broker-list> --sasl-username alice --sasl-password 'S3cretP@ss' --resource-type topic --resource-name foo
  ~~~

- Delete ACLs by filter
  ~~~
  bin/msk-account-cli acl delete --brokers <broker-list> --sasl-username alice --sasl-password 'S3cretP@ss' --resource-type topic --resource-name foo --operation read --permission allow
  ~~~

Consumer groups

- List groups
  ~~~
  bin/msk-account-cli group list --brokers <broker-list> --secret-arn <secret-arn> --region eu-central-1
  ~~~

- Describe group
  ~~~
  bin/msk-account-cli group describe --brokers <broker-list> --sasl-username alice --sasl-password 'S3cretP@ss' --group-id my-group
  ~~~

- Delete groups
  ~~~
  bin/msk-account-cli group delete --brokers <broker-list> --secret-arn <secret-arn> --region eu-central-1 --group-id g1 --group-id g2
  ~~~

Authentication

- Provide credentials either via:
    - `--sasl-username` and `--sasl-password` (if these flags are missing, `sasl_username`/`sasl_password` from the config are used)
    - or `--secret-arn` and `--region` (the tool reads username/password from Secrets Manager and overrides any config values)
- SCRAM mechanism defaults to `sha512`; override with `--scram-mechanism sha256` if needed.

Config file (defaults)

- Provide a `default-config.yaml` with default values.
- Search order:
    1. `./default-config.yaml`
    2. `$XDG_CONFIG_HOME/msk-account-cli/config.yaml`
    3. `$HOME/.config/msk-account-cli/config.yaml`
    4. `$HOME/.msk-account-cli.yaml`
- Example:

  ~~~yaml
  region: eu-central-1
  brokers:
    - b-1.example.kafka.amazonaws.com:9096
    - b-2.example.kafka.amazonaws.com:9096
  output: table
  scram_mechanism: sha512
  # Optional: default SASL credentials (used if --secret-arn is not set and flags are omitted)
  sasl_username: alice
  sasl_password: "S3cretP@ss"
  # Optional: default KMS key for secrets (ID or ARN)
  kms_key_id: arn:aws:kms:eu-central-1:111122223333:key/1234abcd-12ab-34cd-56ef-1234567890ab
  # Optional: default MSK cluster ARN used by cluster-related commands
  cluster_arn: arn:aws:kafka:eu-central-1:111122223333:cluster/dev/abcd-efgh
  ~~~

- Flags override defaults from the config. For example, if `--brokers` is missing, values from the config are used.
- For `account create` the precedence is: `--kms-key-id` > `--create-kms-key` > `config.kms_key_id`. If neither flag nor config provides a KMS key id, the command fails.
- For cluster commands (e.g. `msk list-brokers`, `msk associate-secret`, `msk disassociate-secret`) the precedence is: `--cluster-arn` > `config.cluster_arn`.

IAM Permissions

- Secrets Manager: `secretsmanager:CreateSecret`, `secretsmanager:GetSecretValue`, `secretsmanager:TagResource` (if tagging)
- MSK: `kafka:BatchAssociateScramSecret`, `kafka:BatchDisassociateScramSecret`

Notes

- Secret name must start with `AmazonMSK\_` and use a customer-managed KMS key (non-alias ID/ARN).
- Password can be provided via `--password`, environment variable `MSK_ADMIN_PASSWORD`, or piped on stdin.
