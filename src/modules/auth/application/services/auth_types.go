package services

import (
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/auth/application/ports"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/logging"
)

// Service decides CONNECTs against the tiered auth store and authorizes topic
// access. It depends only on ports — never a concrete adapter.
type Service struct {
	store    ports.AuthStore
	verifier ports.PasswordVerifier
	log      logging.Logger
}
