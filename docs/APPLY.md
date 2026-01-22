Apply: One-Step Account + ACL Provision

Overview
- Reads a YAML plan (see configs/account-with-acl.template.yaml)
- Creates SCRAM secret (optionally creates KMS key), associates to MSK cluster, and applies ACLs.

Build
- make build (produces bin/msk-account-cli)

Run
- bin/msk-account-cli apply -f configs/account-with-acl.template.yaml
- JSON output: add -o json

Dry‑Run
- Preview actions without changes:
  - bin/msk-account-cli apply -f configs/account-with-acl.template.yaml --dry-run

Rollback
- Revert ACLs, disassociate the secret, delete the secret, and schedule KMS deletion if the plan created the key:
  - bin/msk-account-cli apply rollback -f configs/account-with-acl.template.yaml
- Options:
  - --force to delete the secret immediately (no recovery window)
  - --kms-pending-window-days N (default 30) for KMS deletion scheduling

YAML Notes
- aws.region is required (or default-config.yaml provides region)
- account.kms:
  - use_existing_key + kms_key_id: use this KMS key
  - create: true to create a new KMS key (description/multi_region/tags optional)
  - otherwise falls back to default-config.yaml kms_key_id
- account.password or password_from_env must be set
- account.associate_with_cluster requires aws.cluster_arn
- admin_connection:
  - brokers: list of bootstrap brokers (or default-config.yaml brokers)
  - auth: either secret_arn (+region) or sasl_username/sasl_password
  - scram_mechanism: sha512 (default) or sha256
- acls: each entry maps 1:1 to the acl create flags
  - defaults: resource_pattern=literal, permission=allow, host="*"
  - principal supports ${account.username}

Topics
- Optional `topics:` section to ensure/create topics before ACLs
  ```yaml
  topics:
    - name: foo
      partitions: 3              # default 1
      replication_factor: 2      # default 1
      create_if_missing: true    # create topic if not present
      configs:
        cleanup.policy: compact
  ```
- Rollback currently lässt Topics unangetastet (konservativer Standard). Wenn du Topic‑Delete im Rollback willst, sag Bescheid.

Example
- See configs/account-with-acl.template.yaml for a complete, commented template.
