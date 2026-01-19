package config

import (
	"errors"
	"fmt"
	"strings"
)

// Validator provides input validation helpers.
type Validator struct{}

func (Validator) ValidateSecretName(name string) error {
	if name == "" {
		return errors.New("secret name is required")
	}
	if !strings.HasPrefix(name, "AmazonMSK_") {
		return fmt.Errorf("secret name must start with 'AmazonMSK_' (got %q)", name)
	}
	return nil
}

func (Validator) ValidateKMSKeyID(keyID string) error {
	if strings.TrimSpace(keyID) == "" {
		return errors.New("kms-key-id is required and must reference a customer-managed key (not the default key)")
	}
	// Disallow aliases (require key id or ARN)
	if strings.Contains(keyID, ":alias/") || strings.HasPrefix(strings.ToLower(keyID), "alias/") {
		return errors.New("kms-key-id must be a key ID or ARN, not an alias")
	}
	return nil
}

func (Validator) ValidateSecretPayload(username, password string) error {
	if strings.TrimSpace(username) == "" {
		return errors.New("username must be non-empty")
	}
	if strings.TrimSpace(password) == "" {
		return errors.New("password must be non-empty")
	}
	return nil
}

var validResourceTypes = map[string]struct{}{
	"topic": {}, "group": {}, "cluster": {}, "transactionalId": {},
}

func (Validator) ValidateResourceType(t string) error {
	if t == "" {
		return errors.New("resource-type is required")
	}
	if _, ok := validResourceTypes[strings.TrimSpace(t)]; !ok {
		return fmt.Errorf("invalid resource-type %q (must be topic|group|cluster|transactionalId)", t)
	}
	return nil
}

func (Validator) ValidateResourcePattern(p string) error {
	if p == "" {
		return nil
	}
	p = strings.ToLower(p)
	if p != "literal" && p != "prefixed" {
		return fmt.Errorf("invalid resource-pattern %q (must be literal|prefixed)", p)
	}
	return nil
}

var validOperations = map[string]struct{}{
	"read": {}, "write": {}, "create": {}, "delete": {}, "alter": {}, "describe": {},
}

func (Validator) ValidateOperation(op string) error {
	if op == "" {
		return errors.New("operation is required")
	}
	if _, ok := validOperations[strings.ToLower(op)]; !ok {
		return fmt.Errorf("invalid operation %q", op)
	}
	return nil
}

func (Validator) ValidatePermission(p string) error {
	p = strings.ToLower(strings.TrimSpace(p))
	if p == "" {
		return errors.New("permission is required")
	}
	if p != "allow" && p != "deny" {
		return fmt.Errorf("invalid permission %q (must be allow|deny)", p)
	}
	return nil
}
