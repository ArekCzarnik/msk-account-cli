msk-account-cli. 
Production-ready Go application (Go 1.21+) that uses AWS SDK for Go v2 (aws-sdk-go-v2) to manage Amazon MSK SCRAM accounts stored in AWS Secrets Manager, and to manage Kafka ACLs and consumer group IDs against an MSK cluster.

## Goal
Implement a CLI tool named `msk-admin` that can:
1) Create and retrieve SCRAM credentials in AWS Secrets Manager following MSK requirements.
2) Associate (and optionally disassociate) the created secret with an MSK cluster using the MSK API.
3) Connect to the Kafka cluster (bootstrap brokers) and manage:
    - ACLs (create/list/delete)
    - Consumer groups (list/describe/delete) a.k.a. “Group-ID verwalten”

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
    - If `kmsKeyId` is missing, fail with a clear error explaining that default KMS key cannot be used with MSK.
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
- `msk-admin account create`
  Flags:
    - `--region`
    - `--secret-name` (must start with AmazonMSK_)
    - `--kms-key-id` (required; key ID or ARN; do NOT accept alias)
    - `--username`
    - `--password`
    - `--tags` (optional key=value repeated)
      Behavior:
    - Create secret in Secrets Manager with SecretString JSON and KmsKeyId set.
    - Print: secret ARN.

- `msk-admin account get`
  Flags:
    - `--region`
    - `--secret-arn` (or --secret-name)
      Behavior:
    - Retrieve and print username (never print password unless `--show-password` is set).

### MSK association
- `msk-admin msk associate-secret`
  Flags:
    - `--region`
    - `--cluster-arn`
    - `--secret-arn` (repeatable: allow multiple)
      Behavior:
    - Call MSK `BatchAssociateScramSecret` and print result.

- `msk-admin msk disassociate-secret` (optional but nice)
  Flags:
    - `--region`
    - `--cluster-arn`
    - `--secret-arn` (repeatable)
      Behavior:
    - Call MSK `BatchDisassociateScramSecret` and print result.

### ACL management
- `msk-admin acl create`
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

- `msk-admin acl list`
  Flags:
    - same auth/brokers
    - optional filters: resource-type/name, principal, operation
      Behavior:
    - List matching ACLs, print as table.

- `msk-admin acl delete`
  Flags:
    - same as list/create, but used as filter for deletions
      Behavior:
    - Delete matching ACLs, print what was removed.

### Consumer group (Group-ID) management
- `msk-admin group list`
  Flags: brokers + auth
  Behavior: list consumer groups.

- `msk-admin group describe`
  Flags: brokers + auth + `--group-id`
  Behavior: describe group (members, state, assignments, lag if available).

- `msk-admin group delete`
  Flags: brokers + auth + `--group-id` (repeatable)
  Behavior: delete groups, print results.

## Project structure
Generate a clean module layout:
- /cmd/msk-admin/main.go
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

## Documentation snippet to embed into README
Explain the MSK secret requirements:
- "Other type of secrets" conceptually (we just create plaintext JSON)
- Name prefix AmazonMSK_
- MUST use customer-managed KMS key (default key not allowed)
- Format for username/password JSON
- After creation, associate secret to cluster via BatchAssociateScramSecret
  Also mention: if using AWS CLI, kms-key-id should be key ID/ARN not alias (our tool enforces this).

## MSK Admin CLI Usage

- Binary: `bin/msk-admin` (built with `make build`)
- Output: `--output table|json` (default: `table`)

GUI Mode (TUI)

- Interaktive Ansicht mit `tview` starten
  `bin/msk-admin gui`
  - Links: Menü (Accounts, MSK, ACL, Groups)
  - Rechts: Ergebnis-Tabelle/Details
  - Eingabemasken fragen dieselben Parameter ab wie die jeweiligen Commands (z. B. `--region`, `--brokers`, Auth usw.).
  - Taste `q` beendet den GUI‑Modus.

Secrets / Accounts

- Create secret
  `bin/msk-admin account create --region eu-central-1 --secret-name AmazonMSK_alice --kms-key-id arn:aws:kms:eu-central-1:111122223333:key/abcd-... --username alice --password 'S3cretP@ss' --tags env=dev --tags owner=platform`

- Create secret and auto-create a KMS key
  `bin/msk-admin account create --region eu-central-1 --secret-name AmazonMSK_alice --create-kms-key --kms-key-description "MSK SCRAM secrets" --username alice --password 'S3cretP@ss'`

- Get secret (username only)
  `bin/msk-admin account get --region eu-central-1 --secret-arn arn:aws:secretsmanager:eu-central-1:111122223333:secret:AmazonMSK_alice-XXXX`

- Get secret (show password)
  `bin/msk-admin account get --region eu-central-1 --secret-name AmazonMSK_alice --show-password`

- Alle AmazonMSK_ Accounts auflisten (mit ARN)
  `bin/msk-admin account list --region eu-central-1`

- Delete secret (with 30d recovery)
  `bin/msk-admin account delete --region eu-central-1 --secret-name AmazonMSK_alice`

- Delete secret immediately (no recovery, irreversible)
  `bin/msk-admin account delete --region eu-central-1 --secret-name AmazonMSK_alice --force`

- Delete secret and schedule KMS key deletion in 7 days
  `bin/msk-admin account delete --region eu-central-1 --secret-name AmazonMSK_alice --delete-kms-key --kms-pending-window-days 7`

MSK association

- Associate secret(s) to cluster
  `bin/msk-admin msk associate-secret --region eu-central-1 --cluster-arn arn:aws:kafka:eu-central-1:111122223333:cluster/dev/abcd-... --secret-arn arn:aws:secretsmanager:eu-central-1:111122223333:secret:AmazonMSK_alice-XXXX`

- Disassociate secret(s)
  `bin/msk-admin msk disassociate-secret --region eu-central-1 --cluster-arn arn:aws:kafka:eu-central-1:111122223333:cluster/dev/abcd-... --secret-arn arn:aws:secretsmanager:eu-central-1:111122223333:secret:AmazonMSK_alice-XXXX`

MSK Cluster Listing

- Alle MSK‑Cluster (Name + ARN)
  `bin/msk-admin msk list-clusters --region eu-central-1`

- Mit Namenspräfix filtern
  `bin/msk-admin msk list-clusters --region eu-central-1 --name-prefix dev-`

- Zusätzliche Spalten in Tabelle (state/type)
  `bin/msk-admin msk list-clusters --region eu-central-1 --columns name,arn,state,type`

- JSON enthält zusätzliche Felder automatisch (state, type)
  `bin/msk-admin msk list-clusters --region eu-central-1 --output json`

Kafka Broker Listing

- Broker eines Clusters (ID und Endpoints)
  `bin/msk-admin msk list-brokers --region eu-central-1 --cluster-arn arn:aws:kafka:eu-central-1:111122223333:cluster/dev/abcd-...`

ACL management

- Create ACL (allow alice to read topic foo)
  `bin/msk-admin acl create --brokers b-1.example.kafka.amazonaws.com:9096,b-2.example.kafka.amazonaws.com:9096 --secret-arn arn:aws:secretsmanager:eu-central-1:111122223333:secret:AmazonMSK_alice-XXXX --region eu-central-1 --resource-type topic --resource-name foo --principal User:alice --operation read --permission allow`

- List ACLs for topic foo
  `bin/msk-admin acl list --brokers <broker-list> --sasl-username alice --sasl-password 'S3cretP@ss' --resource-type topic --resource-name foo`

- Delete ACLs by filter
  `bin/msk-admin acl delete --brokers <broker-list> --sasl-username alice --sasl-password 'S3cretP@ss' --resource-type topic --resource-name foo --operation read --permission allow`

Consumer groups

- List groups
  `bin/msk-admin group list --brokers <broker-list> --secret-arn <secret-arn> --region eu-central-1`

- Describe group
  `bin/msk-admin group describe --brokers <broker-list> --sasl-username alice --sasl-password 'S3cretP@ss' --group-id my-group`

- Delete groups
  `bin/msk-admin group delete --brokers <broker-list> --secret-arn <secret-arn> --region eu-central-1 --group-id g1 --group-id g2`

Authentication

- Provide credentials via either:
    - `--sasl-username` and `--sasl-password` (wenn diese Flags fehlen, werden `sasl_username`/`sasl_password` aus der Config verwendet)
    - oder `--secret-arn` und `--region` (das Tool liest Benutzer/Passwort aus Secrets Manager und überschreibt etwaige Config‑Werte)
- SCRAM mechanism defaults to `sha512`; override with `--scram-mechanism sha256` if needed.

Config-Datei (Defaults)

- Lege eine `default-config.yaml` mit Standardwerten an.
- Suchreihenfolge:
    1. `./default-config.yaml`
    2. `$XDG_CONFIG_HOME/msk-admin/config.yaml`
    3. `$HOME/.config/msk-admin/config.yaml`
    4. `$HOME/.msk-admin.yaml`
- Beispiel:

  ```yaml
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
  ```

- Flags überschreiben Defaults aus der Config. Wenn z. B. `--brokers` fehlt, werden Werte aus der Config verwendet.
- Für `account create` gilt: `--kms-key-id` > `--create-kms-key` > `config.kms_key_id`. Ist weder Flag gesetzt noch `kms_key_id` konfiguriert, schlägt der Befehl fehl.
- Für Cluster‑befehle (z. B. `msk list-brokers`, `msk associate-secret`, `msk disassociate-secret`) gilt: `--cluster-arn` > `config.cluster_arn`.

IAM Permissions

- Secrets Manager: `secretsmanager:CreateSecret`, `secretsmanager:GetSecretValue`, `secretsmanager:TagResource` (if tagging)
- MSK: `kafka:BatchAssociateScramSecret`, `kafka:BatchDisassociateScramSecret`

Notes

- Secret name must start with `AmazonMSK_` and use a customer-managed KMS key (non-alias ID/ARN).
- Password can be provided via `--password`, environment variable `MSK_ADMIN_PASSWORD`, or piped on stdin.
