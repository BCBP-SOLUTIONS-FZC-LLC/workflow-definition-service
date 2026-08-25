package domain

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ConnectorRestAlias and ConnectorSQLAlias mirror
// workflow-connectors/pkg/connectors/aliasconfig's Endpoint/Query shape —
// duplicated here, not imported, because this repo's vendored
// workflow-connectors version predates that package's exported Validate.
// Switch to importing aliasconfig directly once workflow-connectors is
// published and this repo's go.mod is bumped.
type ConnectorRestAlias struct {
	Alias        string
	Method       string
	BaseURL      string
	PathTemplate string
	Timeout      time.Duration
}

type ConnectorSQLAlias struct {
	Alias      string
	BaseURL    string
	Path       string
	QueryID    string
	ParamCount int
	Timeout    time.Duration
}

func (a ConnectorRestAlias) Validate() error {
	if a.Alias == "" {
		return fmt.Errorf("%w: alias is required", ErrInvalidConnectorAliasInput)
	}
	if !isValidHTTPMethod(a.Method) {
		return fmt.Errorf("%w: invalid method %q", ErrInvalidConnectorAliasInput, a.Method)
	}
	if a.BaseURL == "" {
		return fmt.Errorf("%w: baseURL is required", ErrInvalidConnectorAliasInput)
	}
	if a.PathTemplate == "" {
		return fmt.Errorf("%w: pathTemplate is required", ErrInvalidConnectorAliasInput)
	}
	return nil
}

func (q ConnectorSQLAlias) Validate() error {
	if q.Alias == "" {
		return fmt.Errorf("%w: alias is required", ErrInvalidConnectorAliasInput)
	}
	if q.BaseURL == "" {
		return fmt.Errorf("%w: baseURL is required", ErrInvalidConnectorAliasInput)
	}
	if q.Path == "" {
		return fmt.Errorf("%w: path is required", ErrInvalidConnectorAliasInput)
	}
	if q.QueryID == "" {
		return fmt.Errorf("%w: queryId is required", ErrInvalidConnectorAliasInput)
	}
	if q.ParamCount < 0 {
		return fmt.Errorf("%w: paramCount must be >= 0", ErrInvalidConnectorAliasInput)
	}
	return nil
}

func isValidHTTPMethod(m string) bool {
	switch strings.ToUpper(m) {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}
