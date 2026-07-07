// Package gateway: HTTP entry point that receives API requests and validates
// bearer tokens before dispatching to downstream handlers.
package gateway

import (
	"graphrag-fixture/auth"
	"graphrag-fixture/log"
)

// HandleRequest validates the incoming bearer token and processes the API request.
func HandleRequest(bearerToken string) error {
	log.Info("gateway: received API request")
	if err := auth.CheckCredential(bearerToken); err != nil {
		log.Error("gateway: invalid bearer token", err)
		return err
	}
	log.Info("gateway: request processed successfully")
	return nil
}
