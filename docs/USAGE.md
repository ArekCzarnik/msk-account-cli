MSK Admin CLI Usage

- Binary: `bin/msk-admin` (built with `make build`)
- Output: `--output table|json` (default: `table`)

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

ACL testing

- Verify topic describe permission (non-intrusive)
  `bin/msk-admin acl test --mode describe-topic --topic foo --brokers <broker-list> --secret-arn <secret-arn> --region eu-central-1`

- Verify produce permission (sends one small message)
  `bin/msk-admin acl test --mode produce --topic foo --brokers <broker-list> --sasl-username alice --sasl-password 'S3cretP@ss'`

- Verify consumer group join/topic access (best-effort)
  `bin/msk-admin acl test --mode consume --topic foo --brokers <broker-list> --secret-arn <secret-arn> --region eu-central-1`

Hinweise:
- `describe-topic` prüft, ob Metadaten des Topics abrufbar sind (benötigt i. d. R. DESCRIBE).
- `produce` sendet eine einzelne Test‑Nachricht. Das Topic muss existieren (sofern Auto‑Create deaktiviert ist).
- `consume` validiert vor allem den Group‑Beitritt (READ auf Group). Ohne Nachrichten im Topic kann das reine Topic‑READ ggf. nicht vollständig geprüft werden.

Consumer groups

- List groups
  `bin/msk-admin group list --brokers <broker-list> --secret-arn <secret-arn> --region eu-central-1`

- Describe group
  `bin/msk-admin group describe --brokers <broker-list> --sasl-username alice --sasl-password 'S3cretP@ss' --group-id my-group`

- Delete groups
  `bin/msk-admin group delete --brokers <broker-list> --secret-arn <secret-arn> --region eu-central-1 --group-id g1 --group-id g2`

Authentication

- Provide credentials via either:
  - `--sasl-username` and `--sasl-password`
  - or `--secret-arn` and `--region` (tool fetches from Secrets Manager)
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
