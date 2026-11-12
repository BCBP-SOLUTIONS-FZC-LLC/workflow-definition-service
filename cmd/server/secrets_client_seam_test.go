package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/config"
)

func TestNewSecretsClient_UnsetAddressIsANilInterfaceAndASetAddressBuildsAClient(t *testing.T) {
	unset := &config.Config{SecretsClientTimeout: time.Second}
	svc := newSecretsClient(unset)
	assert.True(t, svc == nil, "an unset OPENBAO_ADDR must give a nil interface, not a typed nil: got %#v", svc)

	set := &config.Config{OpenBaoAddr: "http://openbao:8200", OpenBaoToken: "t", OpenBaoMount: "secret", SecretsClientTimeout: time.Second}
	assert.NotNil(t, newSecretsClient(set))
}
